#!/bin/bash

declare service=com-manh-cp-proactive
declare cluster=cfw200
declare http_match=auto

declare myname="launch.sh"
declare command="$1"
shift


readonly teleImage="quay.io/manhrd/tele:2.2.0"
readonly teleContainerName="tele"

readonly usage="
    example: ${myname} intercept --help 
"

readonly usage_intercept="
Usage arguments:
    example: ${myname} intercept --cluster mycluster --service com-manh-cp-proactive 
    -h|--help                   Help
    -c|--cluster                GKE cluster name
    -s|--service                Kubernetes service you wish to intercept
    -m|--http-match (optional)  http header request key to match on.  See - 
                https://www.getambassador.io/docs/telepresence/latest/reference/intercepts/#intercept-behavior-when-logged-into-ambassador-cloud
"

case "$command" in
    intercept )
        if test $# -eq 0; then
            echo "Error: arguments not provided, to see supported arguments, please use command ${myname} -h|--help"
        fi
        while test $# -gt 0; do
            case "$1" in
                -h|--help )
                    echo  "$usage_intercept"
                    exit 0
                    ;;
                -c|--cluster )
                    shift
                    cluster=$1
                    shift
                    ;;
                -s|--service )
                    shift
                    service=$1
                    shift
                    ;;
                -m|--http-match )
                    shift
                    http_match=$1
                    shift
                    ;;
                * )
                    echo "Error: Unsupported arguments: ${1}, to see supported arguments, please use command ${myname} -h|--help"
                    break
                    ;;
            esac
        done
        ;;
    leave )
        ;;
    quit )
        ;;
    telepresence )
        ;;
    -*|* )
        echo "$usage"
        exit 0
        ;;
esac

echo "command: $command"
echo "cluster: $cluster"
echo "service: $service"
echo "http_match: $http_match"

function getGCloudConfig()
{
    #TODO: validate set and configured
    echo ${HOME}/.config/gcloud/
}

function startTelepresence()
{
    local gcloudConfigPath=$(getGCloudConfig)

    #docker volume create --name telepresence
    
    #mkdir telepresence

        # bind-propagation=rshared
        #         --volume $PWD/telepresence/:/manh/ \
        #,type=bind,consistency=delegated,bind-propagation=rshared
        #        --device /dev/fuse \
        #        --volume /manh/telepresenceRoot \
        #        
    # TODO start if not already running...
    docker run -d \
        --name ${teleContainerName} \
        --cap-add SYS_ADMIN \
        --volume $gcloudConfigPath:/root/.config/gcloud \
        --volume $PWD/telepresence/:/manh/ \
        --volume /var/run/docker.sock:/var/run/docker.sock \
        --network host \
        --env "cluster=${cluster}" \
        --env "service=${service}" \
        --env "http_match=${http_match}" \
        --privileged \
        $teleImage
}

function intercept()
{
    startTelepresence

    docker exec -it ${teleContainerName}  executor intercept
}

function leave()
{
    docker exec -it ${teleContainerName}  executor leave
}

function quit()
{
    leave
    docker rm -f ${teleContainerName}
}

function telepresence()
{
    docker exec -it ${teleContainerName}  telepresence $@
}

eval $command

