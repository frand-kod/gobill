#!/bin/sh
# Container entrypoint of the OpenWrt lab (lab/openwrt.sh): OpenWrt under qemu with two NICs.
#   eth0 (OpenWrt lan) <- tap-lan, bridged with the container NIC on lab-owrt-lan (the hotspot client container)
#   eth1 (OpenWrt wan) <- tap-wan 192.168.77.1/24, masqueraded out of the container.
# RADIUS to 192.168.77.1:1812-1813 is forwarded to gobill on the host ($GOBILL_HOST); CoA/Disconnect
# arriving on the container port 3799 is forwarded to the NAS DAS port 13799. Not 3799: CoovaChilli sends
# accounting from its CoA socket, and a reply to source port 3799 meets the port podman publishes and is lost.
set -eu
EXT=$(ip -o -4 route show default | awk '{print $5; exit}')
LAN=$(ip -o -4 addr show | awk '$4 ~ /^10\.89\.79\./{print $2; exit}')
GOBILL_HOST=${GOBILL_HOST:-$(getent hosts host.containers.internal | awk '{print $1; exit}')}

# /owrt/vm/disk.qcow2 (bind-mounted, kept between container runs) holds the OpenWrt changes over the read-only base
[ -s /owrt/vm/disk.qcow2 ] || qemu-img create -q -f qcow2 -b /owrt/base.img -F raw /owrt/vm/disk.qcow2
ip tuntap add dev tap-wan mode tap
ip tuntap add dev tap-lan mode tap
ip addr add 192.168.77.1/24 dev tap-wan
ip link set tap-wan up

ip link add br-lan type bridge
ip link set tap-lan master br-lan
ip addr flush dev "$LAN"
ip link set "$LAN" master br-lan
ip link set tap-lan up; ip link set "$LAN" up; ip link set br-lan up
# Chilli reads LAN frames raw: with checksum offload the client's TCP arrives with no checksum and is dropped.
# qemu turns the offload on when it opens the tap, so switch it off once qemu runs.
(sleep 5; ethtool -K tap-lan tx off >/dev/null 2>&1) &

iptables -t nat -A POSTROUTING -s 192.168.77.0/24 -o "$EXT" -j MASQUERADE
iptables -t nat -A PREROUTING -i tap-wan -d 192.168.77.1 -p udp --dport 1812:1813 -j DNAT --to-destination "$GOBILL_HOST"
iptables -t nat -A PREROUTING ! -i tap-wan -p udp --dport 3799 -j DNAT --to-destination 192.168.77.2:13799
iptables -t nat -A OUTPUT -p udp --dport 3799 -j DNAT --to-destination 192.168.77.2:13799 # rootlessport delivers from inside the container
iptables -t nat -A POSTROUTING -o tap-wan -j MASQUERADE # OpenWrt sees CoA from 192.168.77.1
dnsmasq --user=root --port=0 --interface=tap-wan --bind-interfaces --dhcp-range=192.168.77.2,192.168.77.2,12h \
	--dhcp-option=3,192.168.77.1 --dhcp-option=6,1.1.1.1 --dhcp-leasefile=/tmp/dnsmasq.leases
echo "$GOBILL_HOST" >/tmp/gobill.host

exec qemu-system-x86_64 -enable-kvm -cpu host -smp 1 -m 256 -display none -monitor none \
	-serial file:/tmp/owrt-console.log \
	-drive file=/owrt/vm/disk.qcow2,format=qcow2,if=virtio \
	-netdev tap,id=l,ifname=tap-lan,script=no,downscript=no -device virtio-net-pci,netdev=l,mac=52:54:00:bb:00:01 \
	-netdev tap,id=w,ifname=tap-wan,script=no,downscript=no -device virtio-net-pci,netdev=w,mac=52:54:00:bb:00:02
