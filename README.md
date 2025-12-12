# Token-Refresher

[![CircleCI](https://circleci.com/gh/observatorium/token-refresher.svg?style=svg)](https://circleci.com/gh/observatorium/token-refresher) [![Go Report Card](https://goreportcard.com/badge/github.com/observatorium/token-refresher)](https://goreportcard.com/report/github.com/observatorium/token-refresher)

`token-refresher` is a helper that fetches and refreshes OAuth2 access tokens via OIDC. It can do two things with these tokens:
1. write them to a file on disk; tokens are refreshed before expiration so that the file always holds a valid token; and
2. provide an HTTP proxy that adds an HTTP `Authorization` header containing the token to all outgoing requests.

`token-refresher` enables applications that only know how to read bearer tokens from a file or make HTTP requests to interface with APIs that are secured with OAuth2.

*Note*: the proxy should be used with care as any client that can access the API can impersonate the configured OAuth2 client.

## Usage

```txt mdox-exec="./token-refresher --help 2>&1 | head -n -1" mdox-expect-exit-code=2
Usage of ./token-refresher:
      --debug.name string                 A name to add as a prefix to log lines. (default "token-refresher")
      --file string                       The path to the file in which to write the retrieved token.
      --log.format string                 The log format to use. Options: 'logfmt', 'json'. (default "logfmt")
      --log.level string                  The log filtering level. Options: 'error', 'warn', 'info', 'debug'. (default "info")
      --margin duration                   The margin of time before a token expires to try to refresh it. (default 5m0s)
      --oidc.audience string              The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.
      --oidc.client-id string             The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.
      --oidc.client-secret string         The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.
      --oidc.issuer-url string            The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.
      --oidc.password string              The password to use for OIDC authentication. If both username and password are set then grant_type is set to password.
      --oidc.username string              The username to use for OIDC authentication. If both username and password are set then grant_type is set to password.
      --scope strings                     The scope to be included in the payload data of the token. Scopes can either be comma-separated or space-separated.
      --temp-file string                  The path to a temporary file to use for atomically update the token file. If left empty, ".tmp" will be suffixed to the token file.
      --upstream.ca-file string           The path to the CA file to verify upstream server TLS certificates.
      --upstream.read-timeout duration    The time from when the connection is accepted to when the request body is fully read.
      --upstream.url string               The target URL to which to proxy requests. All requests will have the acces token in the Authorization HTTP header.
      --upstream.write-timeout duration   The time from the end of the request header read to the end of the response write .
      --url string                        The target URL to which to proxy requests. All requests will have the acces token in the Authorization HTTP header. (DEPRECATED: Use -upstream.url instead)
      --web.internal.listen string        The address on which the internal server listens. (default ":8081")
      --web.listen string                 The address on which the proxy server listens. (default ":8080")
pflag: help requested
```
