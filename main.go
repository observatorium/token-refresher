package main

import (
	"context"
	"fmt"
	"io/ioutil"
	stdlog "log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/metalmatze/signal/healthcheck"
	"github.com/metalmatze/signal/internalserver"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	flag "github.com/spf13/pflag"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type config struct {
	file      string
	logLevel  level.Option
	logFormat string
	margin    time.Duration
	name      string

	oidc   oidcConfig
	server serverConfig
}

type serverConfig struct {
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
	flag.StringVar(&cfg.oidc.issuerURL, "oidc.issuer-url", "", "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	flag.StringVar(&cfg.oidc.clientSecret, "oidc.client-secret", "", "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.clientID, "oidc.client-id", "", "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.audience, "oidc.audience", "", "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")
	flag.StringVar(&cfg.file, "file", "token", "The path to the file in which to write the retrieved token.")
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
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), oauth2.HTTPClient,
			&http.Client{
				Transport: newRoundTripperInstrumenter(reg).NewRoundTripper("oauth", http.DefaultTransport),
			},
		))
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

		g.Add(func() error {
			for {
				t, err := ccc.Token(ctx)
				switch {
				case err != nil:
					level.Error(logger).Log("msg", "failed to get token", "err", err)
				case !t.Valid():
					level.Error(logger).Log("msg", "token is invalid", "exp", t.Expiry.String())
				default:
					if err := ioutil.WriteFile(cfg.file, []byte(t.AccessToken), 0644); err != nil {
						level.Error(logger).Log("msg", "failed to write token to disk", "err", err)
					}
				}
				select {
				case <-time.NewTimer(t.Expiry.Sub(time.Now()) - cfg.margin).C:
				case <-ctx.Done():
					return nil
				}
			}
		}, func(_ error) {
			cancel()
		})
	}

	if err := g.Run(); err != nil {
		stdlog.Fatal(err)
	}
}
