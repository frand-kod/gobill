# PPPoE test client: a userspace client (lab/pppoe), because rootless podman cannot open /dev/ppp.
FROM docker.io/library/alpine:3.22
COPY pppoe /usr/local/bin/pppoe
CMD ["sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1; done"]
