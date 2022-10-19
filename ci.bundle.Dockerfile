FROM golang:1.18 AS builder

# CI should replace this pullspec in manifest files during bundle build
ARG IMAGE_TAG_BASE=quay.io/openstack-k8s-operators/mariadb-operator
ARG VERSION=0.0.1

WORKDIR /mariadb-operator
RUN curl -s -L "https://github.com/operator-framework/operator-sdk/releases/latest/download/operator-sdk_linux_amd64" -o operator-sdk
RUN chmod +x ./operator-sdk
RUN mv ./operator-sdk /usr/local/bin

COPY . .
RUN IMG=${IMAGE_TAG_BASE}:v${VERSION} make manifests build bundle

FROM scratch

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=mariadb-operator
LABEL operators.operatorframework.io.bundle.channels.v1=alpha
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.22.1
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v3

# Labels for testing.
LABEL operators.operatorframework.io.test.mediatype.v1=scorecard+v1
LABEL operators.operatorframework.io.test.config.v1=tests/scorecard/

# Copy files to locations specified by labels.
COPY --from=builder /mariadb-operator/bundle/manifests/ /manifests/
COPY --from=builder /mariadb-operator/bundle/metadata /metadata/
COPY --from=builder /mariadb-operator/bundle/tests/scorecard /tests/scorecard/
