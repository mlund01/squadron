FROM debian:bookworm-slim

ARG VERSION=latest

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl bash && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL https://raw.githubusercontent.com/mlund01/squadron/main/install.sh | bash -s ${VERSION}

ENV SQUADRON_HOME=/data/squadron
ENV PATH="/root/.local/bin:${PATH}"
VOLUME /data/squadron

ENTRYPOINT ["squadron"]
