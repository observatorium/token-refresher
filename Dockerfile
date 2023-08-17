FROM registry.access.redhat.com/ubi8/ubi-minimal:latest as builder

RUN microdnf install go-toolset make

ADD . /opt
WORKDIR /opt
# Run this before `make token-refresher` to be friendy with Docker image layer cache.
RUN make vendor

ARG TARGETOS TARGETARCH

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} make token-refresher

FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

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
