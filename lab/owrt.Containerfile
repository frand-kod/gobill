# OpenWrt under qemu/KVM for lab/openwrt.sh. The image and the overlay disk are bind-mounted at /owrt.
FROM docker.io/library/alpine:3.22
RUN apk add --no-cache qemu-system-x86_64 qemu-img iproute2 iptables dnsmasq openssh-client ethtool
COPY owrt-entry.sh /usr/local/bin/owrt-entry
ENTRYPOINT ["/usr/local/bin/owrt-entry"]
