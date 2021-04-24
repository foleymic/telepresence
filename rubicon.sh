#!/bin/sh

declare myname=`basename $0`
declare version="0.0.2"

declare http_match=auto
declare rubiconDir=~/rubicon
declare rubiconConfig=$rubiconDir/config.properties
declare lastInterceptFile=$rubiconDir/lastIntercept.properties
declare defaultPort=8080
declare defaultDebugPort=5005
declare debug=false
declare fqServicePrefix=com-manh-cp-

declare command="$1" && shift

unameOut="$(uname -s)"
case "${unameOut}" in
    Linux*)     machine=Linux;;
    Darwin*)    machine=Mac;;
    CYGWIN*)    machine=Cygwin;;
    MINGW*)     machine=MinGw;;
    *)          machine="UNKNOWN:${unameOut}"
esac

readonly usage="
    $myname is a specialized MANH wrapper for Ambassador.io's TELEPRESENCE tool.
    Read more about TELEPRESENCE here - https://www.getambassador.io/docs/telepresence/latest/quick-start/
    example: ${myname} intercept --help 

    Usage:
        ${myname} [command]
    Available Commands:
        intercept               Intercept a running service
        debug                   Launch local instance to debug
        leave                   revoke the intercept (telepresence leave {service name})
        quit                    quit all intercepts (telepresence quit)
        version                 Display the version of $myname and telepresence
        help                    Display help
"

readonly usage_intercept="
Usage arguments:
    example: ${myname} ${command} --cluster mycluster --service com-manh-cp-proactive 
    -h|--help                   Help
    -c|--cluster                GKE cluster name
    -s|--service                Kubernetes service you wish to intercept
    -g|--gcloudConfiguration    Set activate gcloud configuration
    -p|--port                   Local port
    -m|--http-match (auto)      http header request key to match on.  See - 
                https://www.getambassador.io/docs/telepresence/latest/reference/intercepts/#intercept-behavior-when-logged-into-ambassador-cloud
    -d|--debug                  Start local instance of the service.
    --docker                    Launch local instance in docker.
    --verbose                   Verbose output
"

readonly usage_leave="
Usage arguments:
    example: ${myname} ${command} --name proactive
    -h|--help                   Help
    -n|--service(optional)      Name of the service for the intercept you wish to leave.  If not supplied, last service intercepted will be exited.
"

readonly usage_debug="
Usage arguments:
    example: ${myname} ${command} --name proactive
    -h|--help                   Help
    -n|--service(optional)      Name of the service for the intercept you wish to debug.  If not supplied, we'll use the last service intercepted.
    --docker                    Launch local instance in docker.
"

function _exit()
{
    local msg=$1
    echo $msg
    echo "Exiting..."
    exit
}

function verbose()
{
    if [ ! -z $verboseMode ]; then
        echo $1
    fi
}

case "$command" in
    intercept )
        if test $# -eq 0; then
            echo "Error: arguments not provided, to see supported arguments, please use command ${myname} -h|--help"
            exit
        fi
        while test $# -gt 0; do
            case "$1" in
                -h|--help )
                    echo  "$usage_intercept"
                    exit 0
                    ;;
                -c|--cluster )
                    shift
                    CLUSTER=$1
                    shift
                    ;;
                -s|--service )
                    shift
                    SERVICE=$1
                    shift
                    ;;
                -g|--gcloudConfiguration )
                    shift
                    gcloudConfiguration=$1
                    shift
                    ;;
                -m|--http-match )
                    shift
                    http_match=$1
                    shift
                    ;;
                -p|--port )
                    shift
                    port=$1
                    shift
                    ;;
                -d|--debug )
                    debug=true
                    shift
                    ;;
                --docker )
                    docker=true
                    shift
                    ;;
                --verbose )
                    verboseMode=true
                    shift
                    ;;
                * )
                    echo "Error: Unsupported arguments: ${1}, to see supported arguments, please use command ${myname} ${command} -h|--help"
                    break
                    ;;
            esac
        done
        ;;
    leave )
        while test $# -gt 0; do
            case "$1" in
                -h|--help )
                    echo  "$usage_leave"
                    exit 0
                    ;;
                -s|--service )
                    shift
                    SERVICE=$1
                    shift
                    ;;
                * )
                    echo "Error: Unsupported arguments: ${1}, to see supported arguments, please use command ${myname} ${command} -h|--help"
                    break
                    ;;
            esac
        done
        ;;
    debug )
        debug=true
        while test $# -gt 0; do
            case "$1" in
                -h|--help )
                    echo  "$usage_debug"
                    exit 0
                    ;;
                -s|--service )
                    shift
                    SERVICE=$1
                    shift
                    ;;
                --docker )
                    docker=true
                    shift
                    ;;
                * )
                    echo "Error: Unsupported arguments: ${1}, to see supported arguments, please use command ${myname} ${command} -h|--help"
                    break
                    ;;
            esac
        done
        ;;

    quit )
        ;;
    tele )
        ;;
    version )
        ;;
    help )
        echo "$usage"
        exit 0
        ;;
    -*|* )
        echo "$usage"
        exit 0
        ;;
esac

function _createRubiconConfig()
{
    echo "Looks like you do not have a config file yet ($rubiconConfig)."
    echo "No problem, we can create one for you.  Just answer a few questions."

    read -p "Root path to your Manhattan components (hopefully you have them all under one parent directory): " repoPath

    echo "MANH_COMPONENTS_PATH=$repoPath" > $rubiconConfig
    source $rubiconConfig
 }

function getFullServiceNameIfNeeded()
{
    if [[ $SERVICE == $fqServicePrefix* ]]; then
        echo $SERVICE
    else
        echo ${fqServicePrefix}${SERVICE}
    fi
}

function getComponentNameFromService()
{
    
    local componentName=${SERVICE#"$fqServicePrefix"}

    echo $componentName
}

function _guessRepoSourceCodePath()
{
    componentName=$(getComponentNameFromService)

    echo $MANH_COMPONENTS_PATH/component-$componentName
}

function _sourceRubiconProjectFile()
{
    if [ -f $lastInterceptFile ]; then 
        source $lastInterceptFile
    fi

    if [ -z $SERVICE ]; then
        SERVICE=$ACTIVE_INTERCEPT
    fi

    if [ -z $CLUSTER ]; then
        CLUSTER=$ACTIVE_CLUSTER
    fi

    SERVICE=$(getFullServiceNameIfNeeded)
    COMPONENT_NAME=$(getComponentNameFromService)
    serviceRootDir=$rubiconDir/$CLUSTER/$COMPONENT_NAME
    mkdir -p $serviceRootDir
    
    configurationFileName="${serviceRootDir}/config.properties"
    envFile=$serviceRootDir/${SERVICE}.env

    if [ -f $configurationFileName ]; then
        source $configurationFileName
    elif [ ! $command = intercept ]; then
        _exit "Uh oh!  I don't seem to have a configuration file for $SERVICE ($configurationFileName)."
    fi

    if [ -f $rubiconConfig ]; then
        source $rubiconConfig
    else
        _createRubiconConfig
    fi

    if [ -z $COMPONENT_REPO_PATH ]; then
        COMPONENT_REPO_PATH=$(_guessRepoSourceCodePath)
        if [ -z docker ]; then
            if [ ! -d $COMPONENT_REPO_PATH ]; then
                read -p "Enter the source code path for $SERVICE or click <return> to use default ports: " COMPONENT_REPO_PATH
            fi 
        fi
    fi
}

function _saveInterceptParms()
{
    local fileName=$1
    echo "########################################################################################" > $fileName
    echo "# GENERATED FILE FROM INTERCEPT CALL ($SERVICE)." >> $fileName
    echo "########################################################################################" >> $fileName
    echo "GCLOUD_CONFIGURATION=$GCLOUD_CONFIGURATION" >> $fileName
    echo "CLUSTER=$CLUSTER" >> $fileName
    echo "SERVICE=$SERVICE" >> $fileName
    echo "COMPONENT_NAME=$COMPONENT_NAME" >> $fileName
    echo "PORT=$PORT" >> $fileName
    echo "DEBUG_PORT=$DEBUG_PORT" >> $fileName
    echo "http_match=$HTTP_MATCH" >> $fileName
    echo "COMPONENT_REPO_PATH=$COMPONENT_REPO_PATH" >> $fileName
    echo "########################################################################################" >> $fileName
    echo

    if [ ! -z $verboseMode ]; then echo && cat $fileName; fi
}

function _saveLocalAndNamedInterceptParms()
{
    echo "ACTIVE_INTERCEPT=$SERVICE" > $lastInterceptFile
    echo "ACTIVE_CLUSTER=$CLUSTER" >> $lastInterceptFile
    _saveInterceptParms $configurationFileName
}

function _getPortFromRepo()
{
    local _port=$defaultPort
    if [ -f $COMPONENT_REPO_PATH/src/main/resources/bootstrap.yml ]; then
        _port=$(grep -A7 'unique:' $COMPONENT_REPO_PATH/src/main/resources/bootstrap.yml | tail -n1 | awk '{ print $2}')
        
        # The above command is a bit of a hack.  Let's go to the defaul if _port is not a number.
        re='^[0-9]+$'
        if ! [[ $_port =~ $re ]] ; then
            _port=$defaultPort
        fi
    fi
    echo $_port
}

function _setPorts()
{
    if [ -z $PORT ]; then
        PORT=$(_getPortFromRepo)
    fi

    if [ $PORT = $defaultPort ]; then
        DEBUG_PORT=$defaultDebugPort
    else
        DEBUG_PORT=$(($PORT + 5))
    fi
}

function _overrideParm()
{
    passedInParm=$1
    sourcedParm=$2
    if [ ! $passedInParm = "" ]; then echo $passedInParm; else echo $sourcedParm; fi
}

function _processArgs()
{
    _sourceRubiconProjectFile

    GCLOUD_CONFIGURATION=$(_overrideParm $gcloudConfiguration $GCLOUD_CONFIGURATION)
    CLUSTER=$(_overrideParm $cluster $CLUSTER)
    PORT=$(_overrideParm $port $PORT)
    DEBUG_PORT=$(_overrideParm $debugPort $DEBUG_PORT)
    HTTP_MATCH=$(_overrideParm $http_match $HTTP_MATCH)

    if [ ! "$HTTP_MATCH" = "auto" ] && [ ! "$HTTP_MATCH" = "all" ] && [ ! "$HTTP_MATCH" = "" ]; then
        HTTP_MATCH_STR="x-telepresence-intercept-id=${HTTP_MATCH}"
    fi

    _setPorts

    if [ "${GCLOUD_CONFIGURATION}" = "" ]; then
        GCLOUD_CONFIGURATION=$(gcloud config configurations list --filter=IS_ACTIVE=true --format="value(name)")
    fi

    verbose "command: $command"
    verbose "gcloud configuration: $GCLOUD_CONFIGURATION"
    verbose "cluster: $CLUSTER"
    verbose "service: $SERVICE"
    verbose "componentName: $COMPONENT_NAME"
    verbose "local port: $PORT" 
    verbose "debug port: $DEBUG_PORT"
    verbose "http_match: $HTTP_MATCH"
    verbose "HTTP_MATCH_STR: $HTTP_MATCH_STR"
    verbose "debug: $debug"
    verbose "MANH_COMPONENTS_PATH: $MANH_COMPONENTS_PATH"
    verbose "COMPONENT_REPO_PATH: $COMPONENT_REPO_PATH"
    
    echo "Configuration properties file: $configurationFileName"
    echo "Service envFile: $envFile"

    _saveLocalAndNamedInterceptParms
}

function validateGCloud()
{
    if ! gcloud -v COMMAND &> /dev/null
    then
        _exit "Please install gcloud sdk - https://cloud.google.com/sdk/install"
    fi

    if ! gcloud config configurations activate $GCLOUD_CONFIGURATION ; then
        _exit "Please see - https://cloud.google.com/sdk/docs/configurations for details on creating gcloud configurations."
    fi

    if [[ $( gcloud components list --filter=id~kubectl$ --format="get(Status)" ) != Installed ]]; then 
        echo "gcloud component - kubectl not installed. but needed for ${myname}";
        echo "Would you like me to install it now? (y/n)"
        read input
        if [ "$input" == "y" ]; then
            echo "Installing gcloud component kubectl..."
            gcloud components install kubectl
        else
            _exit "Goodbye"
        fi 
    fi
} 

function _init()
{
    _processArgs

    validateGCloud

    gcloud container clusters get-credentials $CLUSTER

    telepresence connect
}

function _updateServiceEnvFile()
{
    local _envFile="$1"

    if [ $machine = "Mac" ]; then sedMacHack="''"; fi

    # Update the SPRING_DATASOURCE_URL
    eval "sed -i $sedMacHack 's|SPRING_DATASOURCE_URL=jdbc:mysql:\/\/.*:3306|SPRING_DATASOURCE_URL=jdbc:mysql:\/\/mysql.default:3306|' $_envFile"
    
    # Comment out remote cache 
    eval "sed -i $sedMacHack 's/^CACHE_*/#CACHE_/ ' $_envFile"
    eval "sed -i $sedMacHack 's/^HAZELCAST_PORT*/#HAZELCAST_PORT/ ' $_envFile"
    eval "sed -i $sedMacHack 's/^HAZELCAST_SERVICE*/#HAZELCAST_SERVICE/ ' $_envFile"

    # Enable debug
    eval "sed -i $sedMacHack '/JAVA_OPTS/ s/$/ -agentlib:jdwp=transport=dt_socket,address=${DEBUG_PORT},server=y,suspend=n/' $_envFile"

    # enable sourcing the file
    eval "sed -i $sedMacHack 's/\(JAVA_OPTS=[[:blank:]]*\)\(.*\)/\1\"\2\"/' $_envFile"
    echo "SERVER_PORT=$PORT" >> $_envFile

    # TODO: Update data volume paths...
}

function _dockerDebug()
{
    # Remove quotes from JAVA_OPTS in env file
    if [ $machine = "Mac" ]; then sedMacHack="''"; fi
    eval "sed -i $sedMacHack 's/\"//' ${envFile}"

    image=$(kubectl get deployment $SERVICE -o yaml | grep image: | cut -d ":" -f2,3 | sort | uniq | grep $SERVICE)

    docker run -d \
        --name ${COMPONENT_NAME} \
        -p $PORT:$PORT \
        -p $DEBUG_PORT:$DEBUG_PORT \
        --env-file ${envFile} \
        ${image}

    echo "You can tail the logs by running - docker logs -f ${COMPONENT_NAME}"
}

function _debug()
{
    echo "debugging service: $SERVICE ($COMPONENT_REPO_PATH)"
    echo "DEBUG PORT: ${DEBUG_PORT}"

    echo "Would you like me to continue? (y/n)"
    read input
    if [ "$input" == "y" ]; then
        if [ ! -z docker ]; then
            _dockerDebug
        else
            if [ -f $COMPONENT_REPO_PATH/build.gradle ] ; then
                set -o allexport
                source ${envFile}
                set +o allexport

                eval "$COMPONENT_REPO_PATH/gradlew bootRun -x test -p $COMPONENT_REPO_PATH"
            else
                echo "I cannot find your source code directory for $SERVICE."
                echo "Would you like to launch the docker container instead?  (y/n)"
                read input
                if [ "$input" == "y" ]; then
                    _dockerDebug
                else
                    _exit "Goodbye"
                fi
            fi
        fi
    else
        _exit "Goodbye"
    fi 

}

function intercept()
{
    _init

    telepresence intercept ${SERVICE} \
        --port $PORT \
        --env-file $envFile \
        --preview-url=false \
        --http-match=${HTTP_MATCH_STR} \
        --mount=$serviceRootDir/mnt

    _updateServiceEnvFile $envFile
    
    if [ $debug = "true" ]; then _debug; fi
}

function leave()
{
    _sourceRubiconProjectFile
    telepresence leave ${SERVICE}
}

function debug()
{
    _sourceRubiconProjectFile
    _debug
}

function quit()
{
    leave
    telepresence quit
}

function tele()
{
    telepresence $@
}

function version()
{
    echo "$myname Version: $version"
    echo
    echo "Telepresence version:"
    telepresence version

}

eval $command

