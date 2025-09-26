telepresence intercept com-manh-cp-composer --port="23001:8080" --http-header="x-intercept-id=user1"

telepresence intercept com-manh-cp-composer --port="23002:8080" --http-header="x-intercept-id=user2"








telepresence leave com-manh-cp-composer && \
telepresence uninstall -a && \
telepresence helm uninstall


TELEPRESENCE_TEL2_IMAGE_PLATFORM=linux/amd64 TELEPRESENCE_REGISTRY=quay.io/manhrd make tel2-image
docker push quay.io/manhrd/tel2:2.22.0-alpha.7


telepresence helm install --set logLevel=debug --values values.yaml

telepresence helm install  --values values.yaml

cd /tmp/server1 && python3 -m http.server 23001
