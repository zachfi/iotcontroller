FROM golang:1.25-alpine

ARG TARGETOS
ARG TARGETARCH
ENV GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN apk --update add --no-cache make git bash protoc

WORKDIR /tools
COPY tools /tools
RUN /tools/install.sh
COPY tools/entrypoint.sh /bin/entrypoint.sh
ENTRYPOINT [ "/bin/entrypoint.sh" ]

