// These are the defaults for this component's configuration.
// When calling the function to generate the component's manifest,
// you can pass an object structured like the default to overwrite default values.
local defaults = {
  local defaults = self,
  name: error 'must provide name',
  image: 'quay.io/observatorium/token-refresher',
  version: error 'must provide version',
  namespace: error 'must provide namespace',
  serviceMonitor: false,

  url: error 'must provide target url',
  secretName: 'token-refresher-oidc',
  issuerURLKey: 'issuerURL',
  clientIDKey: 'clientID',
  clientSecretKey: 'clientSecret',
  audienceKey: 'audience',
  logLevel: 'info',
  logFormat: 'logfmt',

  ports: {
    web: 8080,
    internal: 8081,
  },

  commonLabels:: {
    'app.kubernetes.io/name': 'token-refresher',
    'app.kubernetes.io/instance': defaults.name,
    'app.kubernetes.io/version': defaults.version,
  },

  podLabelSelector:: {
    [labelName]: defaults.commonLabels[labelName]
    for labelName in std.objectFields(defaults.commonLabels)
    if !std.setMember(labelName, ['app.kubernetes.io/version'])
  },
};

function(params) {
  local tr = self,

  // Combine the defaults and the passed params to make the component's config.
  config:: defaults + params,

  assert std.isBoolean(tr.config.serviceMonitor),

  service: {
    apiVersion: 'v1',
    kind: 'Service',
    metadata: {
      labels: tr.config.commonLabels,
      name: tr.config.name,
      namespace: tr.config.namespace,
    },
    spec: {
      ports: [
        {
          assert std.isString(name),
          assert std.isNumber(tr.config.ports[name]),

          name: name,
          port: tr.config.ports[name],
          targetPort: tr.config.ports[name],
          protocol: 'TCP',
        }
        for name in std.objectFields(tr.config.ports)
      ],
      selector: tr.config.podLabelSelector,
      type: 'ClusterIP',
    },
  },

  deployment: {
    apiVersion: 'apps/v1',
    kind: 'Deployment',
    metadata: {
      labels: tr.config.commonLabels,
      name: tr.config.name,
      namespace: tr.config.namespace,
    },
    spec: {
      selector: {
        matchLabels: tr.config.podLabelSelector,
      },
      template: {
        metadata: {
          labels: tr.config.commonLabels,
        },
        spec: {
          containers: [
            {
              image: tr.config.image + ':' + tr.config.version,
              name: 'token-refresher',
              args: [
                '--log.level=' + tr.config.logLevel,
                '--log.format=' + tr.config.logFormat,
                '--web.listen=0.0.0.0:%d' % tr.config.ports.web,
                '--web.internal.listen=0.0.0.0:%d' % tr.config.ports.internal,
                '--oidc.audience=$(OIDC_AUDIENCE)',
                '--oidc.client-id=$(OIDC_CLIENT_ID)',
                '--oidc.client-secret=$(OIDC_CLIENT_SECRET)',
                '--oidc.issuer-url=$(OIDC_ISSUER_URL)',
                '--url=' + tr.config.url,
              ],
              env: [
                { name: 'OIDC_AUDIENCE', valueFrom: { secretKeyRef: {
                  key: tr.config.audienceKey,
                  name: tr.config.secretName,
                } } },
                { name: 'OIDC_CLIENT_ID', valueFrom: { secretKeyRef: {
                  key: tr.config.clientIDKey,
                  name: tr.config.secretName,
                } } },
                { name: 'OIDC_CLIENT_SECRET', valueFrom: { secretKeyRef: {
                  key: tr.config.clientSecretKey,
                  name: tr.config.secretName,
                } } },
                { name: 'OIDC_ISSUER_URL', valueFrom: { secretKeyRef: {
                  key: tr.config.issuerURLKey,
                  name: tr.config.secretName,
                } } },
              ],
              ports: [
                { name: name, containerPort: tr.config.ports[name] }
                for name in std.objectFields(tr.config.ports)
              ],
            },
          ],
        },
      },
    },
  },

  serviceMonitor: if tr.config.serviceMonitor == true then {
    apiVersion: 'monitoring.coreos.com/v1',
    kind: 'ServiceMonitor',
    metadata+: {
      name: tr.config.name,
      namespace: tr.config.namespace,
      labels: tr.config.commonLabels,
    },
    spec: {
      selector: {
        matchLabels: tr.config.podLabelSelector,
      },
      endpoints: [
        { port: 'internal' },
      ],
    },
  },
}
