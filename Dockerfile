FROM golang:1.17.6-alpine3.15 as builder

RUN apk add --update --no-cache ca-certificates tzdata git make bash && update-ca-certificates

ADD . /opt
WORKDIR /opt

RUN git update-index --refresh; make token-refresher

FROM redhat/ubi8-micro:8.5-596 as runner

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /opt/token-refresher /bin/token-refresher

ARG BUILD_DATE
ARG VERSION
ARG VCS_REF
ARG DOCKERFILE_PATH

LABEL vendor="Observatorium" \
    name="observatorium/token-refresher" \
    description="Write OAuth2 access tokens to disk" \
    io.k8s.display-name="observatorium/token-refresher" \
    io.k8s.description="Write OAuth2 access tokens to disk" \
    maintainer="Observatorium <team-monitoring@redhat.com>" \
    version="$VERSION" \
    org.label-schema.build-date=$BUILD_DATE \
    org.label-schema.description="Write OAuth2 access tokens to disk" \
    org.label-schema.docker.cmd="docker run --rm observatorium/token-refresher" \
    org.label-schema.docker.dockerfile=$DOCKERFILE_PATH \
    org.label-schema.name="observatorium/token-refresher" \
    org.label-schema.schema-version="1.0" \
    org.label-schema.vcs-branch=$VCS_BRANCH \
    org.label-schema.vcs-ref=$VCS_REF \
    org.label-schema.vcs-url="https://github.com/observatorium/token-refresher" \
    org.label-schema.vendor="observatorium/token-refresher" \
    org.label-schema.version=$VERSION

ENTRYPOINT ["/bin/token-refresher"]
