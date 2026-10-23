# RouterOS CHR under qemu/KVM. The CHR image is bind-mounted at /chr/base.img (read only).
FROM docker.io/library/alpine:3.22
RUN apk add --no-cache qemu-system-x86_64 qemu-img seabios iproute2 iptables dnsmasq openssh-client sshpass
COPY chr-entry.sh /usr/local/bin/chr-entry
COPY chrctl /usr/local/bin/chrctl
ENTRYPOINT ["/usr/local/bin/chr-entry"]
