#!/bin/bash

declare command="$1"


function intercept()
{
    gcloud container clusters get-credentials $cluster

    telepresence connect

    mkdir -p /manh/telepresenceRoot

    matchStr=$http_match
    if [ ! "$http_match" = "auto" ] && [ ! "$http_match" = "all" ] && [ ! "$http_match" = "" ]; then
        matchStr="x-telepresence-intercept-id=${http_match}"
    fi

    telepresence intercept ${service} \
        --port 8080 \
        --env-file /manh/${service}.env \
        --preview-url=false \
        --http-match=${matchStr} \
        --mount=/manh/telepresenceRoot

    # Update the SPRING_DATASOURCE_URL
    sed -i 's|SPRING_DATASOURCE_URL=jdbc:mysql:\/\/.*:3306|SPRING_DATASOURCE_URL=jdbc:mysql:\/\/mysql.default:3306|' /manh/${service}.env

    # Comment out remote cache 
    sed -i 's/^CACHE_*/#CACHE_/ ' /manh/${service}.env
    sed -i 's/^HAZELCAST_PORT*/#HAZELCAST_PORT/ ' /manh/${service}.env
    sed -i 's/^HAZELCAST_SERVICE*/#HAZELCAST_SERVICE/ ' /manh/${service}.env

    # Enable debug
    sed -i '/JAVA_OPTS/ s/$/ -agentlib:jdwp=transport=dt_socket,address=5005,server=y,suspend=n/' /manh/${service}.env

    # enable sourcing the file
    sed -i 's/\(JAVA_OPTS=[[:blank:]]*\)\(.*\)/\1"\2"/' /manh/${service}.env
}

function leave()
{
    telepresence leave ${service}
}

eval $command