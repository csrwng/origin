FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.17-openshift-4.11 AS builder
WORKDIR /go/src/github.com/openshift/origin
COPY . .
RUN make; \
    mkdir -p /tmp/build; \
    cp /go/src/github.com/openshift/origin/openshift-tests /tmp/build/openshift-tests

FROM registry.ci.openshift.org/ocp/4.11:tests
COPY --from=builder /tmp/build/openshift-tests /usr/bin/
