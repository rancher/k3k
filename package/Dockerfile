FROM alpine

ARG BIN_K3K=bin/k3k
ARG BIN_K3KCLI=bin/k3kcli

COPY ${BIN_K3K} /usr/bin/
COPY ${BIN_K3KCLI} /usr/bin/

CMD ["k3k"]
