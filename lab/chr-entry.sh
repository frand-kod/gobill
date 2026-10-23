#!/bin/sh
# Container entrypoint: wire the CHR's two NICs to the container networks, then run qemu.
#   direct: eth(wan) and eth(lan) are bridged to the CHR, which gets 10.89.77.11 by DHCP.
#   nat:    the CHR sits behind this container (192.168.77.2), masqueraded out to lab-wan, no inbound.
set -eu
MODE=${LAB_MODE:-direct}
WAN=$(ip -o -4 addr show | awk '$4 ~ /^10\.89\.77\./{print $2; exit}')
LAN=$(ip -o -4 addr show | awk '$4 ~ /^10\.89\.78\./{print $2; exit}')
MAC_WAN=52:54:00:aa:00:01 MAC_LAN=52:54:00:aa:00:02

qemu-img create -q -f qcow2 -b /chr/base.img -F raw /tmp/chr.qcow2
ip tuntap add dev tap-wan mode tap
ip tuntap add dev tap-lan mode tap
ip link add br-lan type bridge
ip link set "$LAN" master br-lan
ip link set tap-lan master br-lan
ip addr flush dev "$LAN"
ip link set "$LAN" up; ip link set tap-lan up; ip link set br-lan up

if [ "$MODE" = direct ]; then
	ADDR=$(ip -o -4 addr show dev "$WAN" | awk '{print $4}')
	GW=$(ip route | awk '/^default/{print $3}')
	MAC=$(cat /sys/class/net/"$WAN"/address)
	ip link add br-wan type bridge
	ip link set br-wan address "$MAC"
	ip addr flush dev "$WAN"
	ip link set "$WAN" master br-wan
	ip link set tap-wan master br-wan
	ip link set tap-wan up; ip link set br-wan up
	ip addr add "$ADDR" dev br-wan
	[ -n "$GW" ] && ip route add default via "$GW" || true
	dnsmasq --user=root --port=0 --interface=br-wan --bind-interfaces --dhcp-range=10.89.77.11,10.89.77.11,12h \
		--dhcp-host=$MAC_WAN,10.89.77.11 --dhcp-option=3 --dhcp-leasefile=/tmp/dnsmasq.leases
	echo 10.89.77.11 >/tmp/chr.ip
else
	ip addr add 192.168.77.1/24 dev tap-wan
	ip link set tap-wan up
	iptables -t nat -A POSTROUTING -s 192.168.77.0/24 -o "$WAN" -j MASQUERADE
	iptables -A FORWARD -i tap-wan -j ACCEPT
	iptables -A FORWARD -o tap-wan -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
	iptables -P FORWARD DROP
	dnsmasq --user=root --port=0 --interface=tap-wan --bind-interfaces --dhcp-range=192.168.77.2,192.168.77.2,12h \
		--dhcp-host=$MAC_WAN,192.168.77.2 --dhcp-option=3,192.168.77.1 --dhcp-leasefile=/tmp/dnsmasq.leases
	echo 192.168.77.2 >/tmp/chr.ip
fi

exec qemu-system-x86_64 -enable-kvm -cpu host -smp 1 -m 256 -display none -monitor none \
	-serial file:/tmp/chr-console.log \
	-drive file=/tmp/chr.qcow2,format=qcow2,if=virtio \
	-netdev tap,id=w,ifname=tap-wan,script=no,downscript=no -device virtio-net-pci,netdev=w,mac=$MAC_WAN \
	-netdev tap,id=l,ifname=tap-lan,script=no,downscript=no -device virtio-net-pci,netdev=l,mac=$MAC_LAN
