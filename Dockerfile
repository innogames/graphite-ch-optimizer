# Image for building packages in docker
FROM golang:1.13-alpine as builder

RUN apk --no-cache add ruby ruby-dev ruby-etc make gcc g++ rpm git tar && \
    gem install --no-user-install --no-document fpm



FROM builder AS build
COPY . ./graphite-ch-optimizer

RUN cd graphite-ch-optimizer && \
    make -e CGO_ENABLED=0 build



FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata && mkdir /graphite-ch-optimizer

WORKDIR /graphite-ch-optimizer

COPY --from=build /go/graphite-ch-optimizer/graphite-ch-optimizer .

ENTRYPOINT ["./graphite-ch-optimizer"]
