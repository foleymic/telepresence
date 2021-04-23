FROM quay.io/manhrd/ma-cp-ubuntu:latest

LABEL maintainer="Manhattan Associates"

# update gcloud
RUN /gcloud/google-cloud-sdk/bin/gcloud components update --quiet \
    && gcloud components install kubectl --quiet \
    && curl -fL https://app.getambassador.io/download/tel2/linux/amd64/latest/telepresence -o /usr/local/bin/telepresence \
    && chmod a+x /usr/local/bin/telepresence

RUN curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg \
    && echo \
        "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu focal stable" \
        | tee /etc/apt/sources.list.d/docker.list > /dev/null

RUN apt update \
    && apt install -y sshfs iputils-ping iptables docker-ce-cli

ADD linux/amd64/telepresence /usr/local/bin/telepresence
ADD executor.sh /usr/local/bin/executor
ADD entrypoint.sh /entrypoint.sh


RUN chmod a+x /usr/local/bin/telepresence \
    && chmod a+x /usr/local/bin/executor \
    && chmod a+x /entrypoint.sh

ADD linux/fuse.conf /etc/fuse.conf

ENV cluster, service, http_match

ENTRYPOINT [ "/entrypoint.sh" ]
