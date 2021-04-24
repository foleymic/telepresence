FROM --platform=${BUILDPLATFORM} golang:1.15-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
WORKDIR /telepresence
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN apk add --update --no-cache make
ENV TELEPRESENCE_VERSION=v2.2.0
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} make build

FROM scratch AS bin
ARG TARGETOS
ARG TARGETARCH
COPY --from=build /telepresence/build-output/bin /build-output/${TARGETOS}/${TARGETARCH}/


#############################################################################################
# See - https://www.docker.com/blog/containerize-your-go-developer-environment-part-1/
#    export DOCKER_BUILDKIT=1
#    docker build --platform linux/amd64 --target bin --output bin/ .
#
#    docker build -t tele bin/build-output/
#############################################################################################

#   docker run  --rm -it --name tele  --cap-add NET_ADMIN --cap-add NET_BIND_SERVICE --network host tele bash

#   docker run --rm -it --name tele -v /Users/mfoley/.config/gcloud/:/root/.config/gcloud --cap-add NET_ADMIN --cap-add NET_BIND_SERVICE --network host tele bash

#   docker run --rm -it --name tele -v /Users/mfoley/.config/gcloud/:/root/.config/gcloud --privileged --network host tele bash

# service=com-manh-cp-proactive
# tag=0.67.0-c7332fc-2103121136
# docker run -d \
#   --name ${service} \
#   -p 8080:8080 \
#   -p 5005:5005 \
# --env-file ${service}.env  --network host quay.io/manhrd/${service}:${tag}


# docker run -d \
# --name tele -v /Users/mfoley/.config/gcloud/:/root/.config/gcloud \
# --network host \
# -e "service=com-manh-cp-proactive" -e "cluster=cfw200" \
# --init \
# --privileged \
# tele

