local tr = (import '../lib/token-refresher.libsonnet')({
  name: 'example-token-refresher',
  namespace: 'observatroium',
  version: 'master-2021-03-05-b34376b',
  url: 'http://observatorium-observatorium-api.observatorium.svc:8080',
  secretName: 'token-refresher-oidc',
  serviceMonitor: true,
});

{ [name]: tr[name] for name in std.objectFields(tr) }
