telepresence intercept com-manh-cp-composer --port="23001:8080" --http-header="x-intercept-id=user1"

telepresence intercept com-manh-cp-composer --port="23002:8080" --http-header="x-intercept-id=user2"

telepresence intercept com-manh-cp-composer --port="23003:8080" --http-header="x-intercept-id=user3"






telepresence leave com-manh-cp-composer && \
telepresence uninstall -a && \
telepresence helm uninstall



TELEPRESENCE_TEL2_IMAGE_PLATFORM=linux/amd64 TELEPRESENCE_REGISTRY=quay.io/manhrd make tel2-image
docker push quay.io/manhrd/tel2:2.22.0-alpha.7


telepresence helm install --set logLevel=debug --values values.yaml

telepresence helm install  --values values.yaml

python3 examples/localTesting/test_server.py --port 23001





#  Licensed telepresence on mtstackdm3
telepresence intercept com-manh-cp-composer \
    --port=23001:8080 \
    --env-file=/Users/mfoley/rubikon/mtstackdm3/com-manh-cp-composer/com-manh-cp-composer.env \
    --preview-url=false \
    --http-header=x-intercept-id=mfoley::.*
    --mount=/Users/mfoley/rubikon/mtstackdm3/com-manh-cp-composer/mnt


# Using Deployment com-manh-cp-composer
#   Intercept name         : com-manh-cp-composer
#   State                  : ACTIVE
#   Workload kind          : Deployment
#   Destination            : 127.0.0.1:23001
#   Service Port Identifier: eighty-eighty
#   Volume Mount Point     : /Users/mfoley/rubikon/mtstackdm3/com-manh-cp-composer/mnt
#   Intercepting           : HTTP requests with headers
#         'x-intercept-id =~ mfoley::.*'
s


# Change to scnextgen stack
gcloud config configurations activate dm-c1f
gcloud container clusters get-credentials scnxtg2506
