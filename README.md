# Token-Refresher

[![CircleCI](https://circleci.com/gh/observatorium/token-refresher.svg?style=svg)](https://circleci.com/gh/observatorium/token-refresher)
[![Go Report Card](https://goreportcard.com/badge/github.com/observatorium/token-refresher)](https://goreportcard.com/report/github.com/observatorium/token-refresher)

`token-refresher` is a daemon that fetches OAuth2 access tokens via OIDC and writes them to a file on disk.
The token is refreshed before expiration so that the token in the file is always valid.
This workflow enables applications that only know how to read bearer tokens from a file to interface with APIs that use OAuth2 for authentication.

## Usage

[embedmd]:# (tmp/help.txt)
```txt
Usage of ./token-refresher:
      --debug.name string            A name to add as a prefix to log lines. (default "token-refresher")
      --file string                  The path to the file in which to write the retrieved token. (default "token")
      --log.format string            The log format to use. Options: 'logfmt', 'json'. (default "logfmt")
      --log.level string             The log filtering level. Options: 'error', 'warn', 'info', 'debug'. (default "info")
      --margin duration              The margin of time before a token expires to try to refresh it. (default 5m0s)
      --oidc.audience string         The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.
      --oidc.client-id string        The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.
      --oidc.client-secret string    The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.
      --oidc.issuer-url string       The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.
      --web.internal.listen string   The address on which the internal server listens. (default ":8081")
```
