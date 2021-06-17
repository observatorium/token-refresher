package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	stdlog "log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/metalmatze/signal/healthcheck"
	"github.com/metalmatze/signal/internalserver"
	"github.com/metalmatze/signal/server/signalhttp"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	flag "github.com/spf13/pflag"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const (
	retryInterval = 5 * time.Second
	prefixHeader  = "X-Forwarded-Prefix"
)

type config struct {
	file      string
	logLevel  level.Option
	logFormat string
	margin    time.Duration
	name      string
	tempFile  string
	url       *url.URL

	oidc   oidcConfig
	server serverConfig
}

type serverConfig struct {
	listen         string
	listenInternal string
}

type oidcConfig struct {
	audience     string
	clientID     string
	clientSecret string
	issuerURL    string
}

func parseFlags() (*config, error) {
	cfg := &config{}
	flag.StringVar(&cfg.name, "debug.name", "token-refresher", "A name to add as a prefix to log lines.")
	logLevelRaw := flag.String("log.level", "info", "The log filtering level. Options: 'error', 'warn', 'info', 'debug'.")
	flag.StringVar(&cfg.logFormat, "log.format", "logfmt", "The log format to use. Options: 'logfmt', 'json'.")
	flag.StringVar(&cfg.server.listenInternal, "web.internal.listen", ":8081", "The address on which the internal server listens.")
	flag.StringVar(&cfg.server.listen, "web.listen", ":8080", "The address on which the proxy server listens.")
	flag.StringVar(&cfg.oidc.issuerURL, "oidc.issuer-url", "", "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	flag.StringVar(&cfg.oidc.clientSecret, "oidc.client-secret", "", "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.clientID, "oidc.client-id", "", "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.audience, "oidc.audience", "", "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")
	flag.StringVar(&cfg.file, "file", "", "The path to the file in which to write the retrieved token.")
	flag.StringVar(&cfg.tempFile, "temp-file", "", "The path to a temporary file to use for atomically update the token file. If left empty, \".tmp\" will be suffixed to the token file.")
	rawURL := flag.String("url", "", "The target URL to which to proxy requests. All requests will have the acces token in the Authorization HTTP header.")
	flag.DurationVar(&cfg.margin, "margin", 5*time.Minute, "The margin of time before a token expires to try to refresh it.")

	flag.Parse()

	switch *logLevelRaw {
	case "error":
		cfg.logLevel = level.AllowError()
	case "warn":
		cfg.logLevel = level.AllowWarn()
	case "info":
		cfg.logLevel = level.AllowInfo()
	case "debug":
		cfg.logLevel = level.AllowDebug()
	default:
		return nil, fmt.Errorf("unexpected log level: %s", *logLevelRaw)
	}

	if *rawURL != "" {
		u, err := url.Parse(*rawURL)
		if err != nil {
			return nil, err
		}
		cfg.url = u
	}

	if cfg.file == "" && cfg.url == nil {
		return nil, errors.New("one of --file or --url is required")
	}

	if cfg.tempFile == "" {
		cfg.tempFile = cfg.file + ".tmp"
	}

	return cfg, nil
}

func main() {
	cfg, err := parseFlags()
	if err != nil {
		stdlog.Fatal(err)
	}

	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))

	if cfg.logFormat == "json" {
		logger = log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
	}

	logger = level.NewFilter(logger, cfg.logLevel)

	if cfg.name != "" {
		logger = log.With(logger, "name", cfg.name)
	}

	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
	defer level.Info(logger).Log("msg", "exiting")

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	level.Info(logger).Log("msg", "token-refresher")
	var g run.Group
	{
		// Signal channels must be buffered.
		sig := make(chan os.Signal, 1)
		g.Add(func() error {
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			level.Info(logger).Log("msg", "caught interrupt")
			return nil
		}, func(_ error) {
			close(sig)
		})
	}
	{
		healthchecks := healthcheck.NewMetricsHandler(healthcheck.NewHandler(), reg)
		h := internalserver.NewHandler(
			internalserver.WithName("Internal - token-refresher API"),
			internalserver.WithHealthchecks(healthchecks),
			internalserver.WithPrometheusRegistry(reg),
			internalserver.WithPProf(),
		)

		s := http.Server{
			Addr:    cfg.server.listenInternal,
			Handler: h,
		}

		g.Add(func() error {
			level.Info(logger).Log("msg", "starting internal HTTP server", "address", s.Addr)
			return s.ListenAndServe()
		}, func(err error) {
			_ = s.Shutdown(context.Background())
		})
	}
	{
		provider, err := oidc.NewProvider(context.Background(), cfg.oidc.issuerURL)
		if err != nil {
			stdlog.Fatalf("OIDC provider initialization failed: %v", err)
		}
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient,
			&http.Client{
				Transport: newRoundTripperInstrumenter(reg).NewRoundTripper("oauth", http.DefaultTransport),
			},
		)
		ccc := clientcredentials.Config{
			ClientID:     cfg.oidc.clientID,
			ClientSecret: cfg.oidc.clientSecret,
			TokenURL:     provider.Endpoint().TokenURL,
		}
		if cfg.oidc.audience != "" {
			ccc.EndpointParams = url.Values{
				"audience": []string{cfg.oidc.audience},
			}
		}

		if cfg.file != "" {
			ctx, cancel := context.WithCancel(ctx)
			g.Add(func() error {
				for {
					d := retryInterval
					t, err := ccc.Token(ctx)
					switch {
					case err != nil:
						level.Error(logger).Log("msg", "failed to get token", "err", err)
					case !t.Valid():
						level.Error(logger).Log("msg", "token is invalid", "exp", t.Expiry.String())
					default:
						if err := ioutil.WriteFile(cfg.tempFile, []byte(t.AccessToken), 0644); err != nil {
							level.Error(logger).Log("msg", "failed to write token to temporary file", "err", err)
							break
						}
						if err := os.Rename(cfg.tempFile, cfg.file); err != nil {
							level.Error(logger).Log("msg", "failed to write token to file", "err", err)
							break
						}
						d = t.Expiry.Sub(time.Now()) - cfg.margin
					}
					select {
					case <-time.NewTimer(d).C:
					case <-ctx.Done():
						return nil
					}
				}
			}, func(_ error) {
				cancel()
			})
		}

		if cfg.url != nil {
			ctx, cancel := context.WithCancel(ctx)
			// Create Reverse Proxy.
			p := httputil.ReverseProxy{
				Director: func(request *http.Request) {
					request.URL.Scheme = cfg.url.Scheme
					// Set the Host at both request and request.URL objects.
					request.Host = cfg.url.Host
					request.URL.Host = cfg.url.Host
					// Derive path from the paths of configured URL and request URL.
					request.URL.Path, request.URL.RawPath = joinURLPath(cfg.url, request.URL)
					// Add prefix header with value "/", since from a client's perspective
					// we are forwarding /<anything> to /<cfg.url.Path>/<anything>.
					request.Header.Add(prefixHeader, "/")
				},
			}
			p.Transport = &oauth2.Transport{
				Source: ccc.TokenSource(ctx),
			}
			s := http.Server{
				Addr:    cfg.server.listen,
				Handler: signalhttp.NewHandlerInstrumenter(reg, nil).NewHandler(nil, &p),
			}
			g.Add(func() error {
				level.Info(logger).Log("msg", "starting proxy server", "address", s.Addr)
				return s.ListenAndServe()
			}, func(err error) {
				_ = s.Shutdown(context.Background())
				cancel()
			})
		}
	}

	if err := g.Run(); err != nil {
		stdlog.Fatal(err)
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func joinURLPath(a, b *url.URL) (path, rawpath string) {
	if a.RawPath == "" && b.RawPath == "" {
		return singleJoiningSlash(a.Path, b.Path), ""
	}
	apath := a.EscapedPath()
	bpath := b.EscapedPath()
	aslash := strings.HasSuffix(apath, "/")
	bslash := strings.HasPrefix(bpath, "/")
	switch {
	case aslash && bslash:
		return a.Path + b.Path[1:], apath + bpath[1:]
	case !aslash && !bslash:
		return a.Path + "/" + b.Path, apath + "/" + bpath
	}
	return a.Path + b.Path, apath + bpath
}
