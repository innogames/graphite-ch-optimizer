#
# Image for building packages in docker
# innogames/graphite-ch-optimizer:builder on docker-hub
#
FROM golang:1.13-alpine as builder

RUN apk --no-cache add ruby ruby-dev ruby-etc make gcc g++ rpm git tar && \
    gem install --no-user-install --no-document fpm


#
# Image which contains the binary artefacts
#
FROM builder AS build
COPY . ./graphite-ch-optimizer

WORKDIR ./graphite-ch-optimizer

RUN make test && \
    make -e CGO_ENABLED=0 build && \
    make -e CGO_ENABLED=0 packages

# This one will return tar stream of binary artefacts to unpack on the local file system
CMD ["/usr/bin/tar", "-c", "--exclude=build/pkg", "build", "graphite-ch-optimizer"]


#
# Application image
#
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata && mkdir /graphite-ch-optimizer

WORKDIR /graphite-ch-optimizer

COPY --from=build /go/graphite-ch-optimizer/graphite-ch-optimizer .

ENTRYPOINT ["./graphite-ch-optimizer"]
