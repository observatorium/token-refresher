package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

	oidc     oidcConfig
	server   serverConfig
	upstream upstreamConfig
	scope    []string
}

type upstreamConfig struct {
	url          *url.URL
	caFile       string
	readTimeout  time.Duration
	writeTimeout time.Duration
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
	username     string
	password     string
}

func LookupEnvOrString(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

func LookupEnvOrDuration(key string, defaultVal time.Duration) time.Duration {
	if val, ok := os.LookupEnv(key); ok {
		d, err := time.ParseDuration(val)
		if err != nil {
			fmt.Sprintln("error trying to parse duration, using default value: ", err)
			return defaultVal
		}
		return d
	}
	return defaultVal
}

func parseFlags() (*config, error) {
	cfg := &config{}
	flag.StringVar(&cfg.name, "debug.name", "token-refresher", "A name to add as a prefix to log lines.")
	logLevelRaw := flag.String("log.level", LookupEnvOrString("LOG_LEVEL", "info"), "The log filtering level. Options: 'error', 'warn', 'info', 'debug'.")
	flag.StringVar(&cfg.logFormat, "log.format", LookupEnvOrString("LOG_FORMAT", "logfmt"), "The log format to use. Options: 'logfmt', 'json'.")
	flag.StringVar(&cfg.server.listenInternal, "web.internal.listen", LookupEnvOrString("WEB_INTERNAL_LISTEN", ":8081"), "The address on which the internal server listens.")
	flag.StringVar(&cfg.server.listen, "web.listen", LookupEnvOrString("WEB_LISTEN", ":8080"), "The address on which the proxy server listens.")
	flag.StringVar(&cfg.oidc.issuerURL, "oidc.issuer-url", LookupEnvOrString("OIDC_ISSUER_URL", ""), "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	flag.StringVar(&cfg.oidc.clientSecret, "oidc.client-secret", LookupEnvOrString("OIDC_CLIENT_SECRET", ""), "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.clientID, "oidc.client-id", LookupEnvOrString("OIDC_CLIENT_ID", ""), "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.audience, "oidc.audience", LookupEnvOrString("OIDC_AUDIENCE", ""), "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")
	flag.StringVar(&cfg.oidc.username, "oidc.username", "", "The username to use for OIDC authentication. If both username and password are set then grant_type is set to password.")
	flag.StringVar(&cfg.oidc.password, "oidc.password", "", "The password to use for OIDC authentication. If both username and password are set then grant_type is set to password.")
	flag.StringSliceVar(&cfg.scope, "scope", []string{}, "The scope to be included in the payload data of the token. Scopes can either be comma-separated or space-separated.")
	flag.StringVar(&cfg.file, "file", LookupEnvOrString("FILE", ""), "The path to the file in which to write the retrieved token.")
	flag.StringVar(&cfg.tempFile, "temp-file", LookupEnvOrString("TEMP_FILE", ""), "The path to a temporary file to use for atomically update the token file. If left empty, \".tmp\" will be suffixed to the token file.")
	rawURL := flag.String("url", LookupEnvOrString("URL", ""), "The target URL to which to proxy requests. All requests will have the acces token in the Authorization HTTP header. (DEPRECATED: Use -upstream.url instead)")
	rawUpstreamURL := flag.String("upstream.url", "", "The target URL to which to proxy requests. All requests will have the acces token in the Authorization HTTP header.")
	flag.StringVar(&cfg.upstream.caFile, "upstream.ca-file", "", "The path to the CA file to verify upstream server TLS certificates.")
	flag.DurationVar(&cfg.upstream.readTimeout, "upstream.read-timeout", 0, "The time from when the connection is accepted to when the request body is fully read.")
	flag.DurationVar(&cfg.upstream.writeTimeout, "upstream.write-timeout", 0, "The time from the end of the request header read to the end of the response write .")
	flag.DurationVar(&cfg.margin, "margin", LookupEnvOrDuration("", 5*time.Minute), "The margin of time before a token expires to try to refresh it.")

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

	if *rawURL != "" && *rawUpstreamURL != "" {
		return nil, errors.New("use only one of --url or --upstream.url")
	}

	if *rawURL != "" {
		u, err := url.Parse(*rawURL)
		if err != nil {
			return nil, err
		}
		cfg.upstream.url = u
	}

	if *rawUpstreamURL != "" {
		u, err := url.Parse(*rawUpstreamURL)
		if err != nil {
			return nil, err
		}
		cfg.upstream.url = u
	}

	if cfg.file == "" && cfg.upstream.url == nil {
		return nil, errors.New("one of --file or --upstream.url is required")
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
		if cfg.oidc.username != "" && cfg.oidc.password != "" {
			ccc.EndpointParams = url.Values{
				"username":   []string{cfg.oidc.username},
				"password":   []string{cfg.oidc.password},
				"grant_type": []string{"password"},
			}
		}
		if len(cfg.scope) != 0 {
			ccc.EndpointParams = url.Values{
				"scope": []string{strings.Join(cfg.scope, " ")},
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

		if cfg.upstream.url != nil {
			ctx, cancel := context.WithCancel(ctx)
			// Create Reverse Proxy.
			p := httputil.ReverseProxy{
				Director: func(request *http.Request) {
					request.URL.Scheme = cfg.upstream.url.Scheme
					// Set the Host at both request and request.URL objects.
					request.Host = cfg.upstream.url.Host
					request.URL.Host = cfg.upstream.url.Host
					// Derive path from the paths of configured URL and request URL.
					request.URL.Path, request.URL.RawPath = joinURLPath(cfg.upstream.url, request.URL)
					// Add prefix header with value "/", since from a client's perspective
					// we are forwarding /<anything> to /<cfg.upstream.url.Path>/<anything>.
					request.Header.Add(prefixHeader, "/")
				},
			}

			base := http.DefaultTransport
			if cfg.upstream.caFile != "" {
				caCert, err := ioutil.ReadFile(cfg.upstream.caFile)
				if err != nil {
					stdlog.Fatalf("failed to initialize upstream server TLS CA: %v", err)
				}
				pool := x509.NewCertPool()
				pool.AppendCertsFromPEM(caCert)

				t := http.DefaultTransport.(*http.Transport).Clone()
				t.TLSClientConfig = &tls.Config{RootCAs: pool}
				base = t
			}

			p.Transport = &oauth2.Transport{
				Source: ccc.TokenSource(ctx),
				Base:   base,
			}
			s := http.Server{
				Addr:    cfg.server.listen,
				Handler: signalhttp.NewHandlerInstrumenter(reg, nil).NewHandler(nil, &p),
			}

			if cfg.upstream.readTimeout != 0 {
				s.ReadTimeout = cfg.upstream.readTimeout
			}

			if cfg.upstream.writeTimeout != 0 {
				s.WriteTimeout = cfg.upstream.writeTimeout
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
