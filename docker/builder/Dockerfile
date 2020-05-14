#
# Image for building packages in docker
# innogames/graphite-ch-optimizer:builder on docker-hub
#
FROM golang:1.13-alpine as builder

RUN apk --no-cache add ruby ruby-dev ruby-etc make gcc g++ rpm git tar && \
    gem install --no-user-install --no-document fpm
