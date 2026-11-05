#!/bin/sh
# Runs inside a fresh OpenWrt (lab/openwrt.sh up): uspot in RADIUS mode against gobill, as in
# docs/en/openwrt.md. The RADIUS dictionaries are copied to /etc/radcli by openwrt.sh beforehand.
#   $1 RADIUS secret   gobill is reached at 192.168.77.1 (forwarded by lab/owrt-entry.sh)
set -eu
SECRET=$1

# the WAN may still be coming up after boot: retry
for i in 1 2 3 4 5 6 7 8 9 10; do
	apk update </dev/null >/dev/null 2>&1 && apk add uspot uspot-www ratelimit </dev/null >/dev/null && break
	[ "$i" = 10 ] && exit 1
	sleep 5
done

# the LAN becomes the 'captive' interface; the lab manages OpenWrt over the WAN (ssh) from now on
uci -q delete network.lan || true
uci set network.captive=interface
uci set network.captive.device=br-lan
uci set network.captive.proto=static
uci set network.captive.ipaddr=10.0.0.1
uci set network.captive.netmask=255.255.252.0
uci -q del_list firewall.@zone[0].network=lan || true
uci -q delete dhcp.lan || true
uci set dhcp.captive=dhcp
uci set dhcp.captive.interface=captive
uci set dhcp.captive.start=2
uci set dhcp.captive.limit=1000
uci set dhcp.captive.leasetime=2h

# firewall: the uspot README rules, plus ssh and DAS (port 13799, see lab/owrt-entry.sh) from the WAN
uci batch <<'B'
add firewall zone
set firewall.@zone[-1].name='captive'
add_list firewall.@zone[-1].network='captive'
set firewall.@zone[-1].input='REJECT'
set firewall.@zone[-1].output='ACCEPT'
set firewall.@zone[-1].forward='REJECT'
add firewall redirect
set firewall.@redirect[-1].name='Redirect-unauth-captive-CPD'
set firewall.@redirect[-1].src='captive'
set firewall.@redirect[-1].src_dport='80'
set firewall.@redirect[-1].proto='tcp'
set firewall.@redirect[-1].target='DNAT'
set firewall.@redirect[-1].reflection='0'
set firewall.@redirect[-1].ipset='!uspot'
add firewall rule
set firewall.@rule[-1].name='Allow-DHCP-NTP-captive'
set firewall.@rule[-1].src='captive'
set firewall.@rule[-1].proto='udp'
set firewall.@rule[-1].dest_port='67 123'
set firewall.@rule[-1].target='ACCEPT'
add firewall rule
set firewall.@rule[-1].name='Restrict-input-captive'
set firewall.@rule[-1].src='captive'
set firewall.@rule[-1].dest_ip='!captive'
set firewall.@rule[-1].target='DROP'
add firewall rule
set firewall.@rule[-1].name='Allow-captive-CPD-WEB-UAM'
set firewall.@rule[-1].src='captive'
set firewall.@rule[-1].dest_port='80 443 3990'
set firewall.@rule[-1].proto='tcp'
set firewall.@rule[-1].target='ACCEPT'
add firewall rule
set firewall.@rule[-1].name='Forward-auth-captive'
set firewall.@rule[-1].src='captive'
set firewall.@rule[-1].dest='wan'
set firewall.@rule[-1].proto='any'
set firewall.@rule[-1].target='ACCEPT'
set firewall.@rule[-1].ipset='uspot'
add firewall rule
set firewall.@rule[-1].name='Allow-DNS-captive'
set firewall.@rule[-1].src='captive'
add_list firewall.@rule[-1].proto='udp'
add_list firewall.@rule[-1].proto='tcp'
set firewall.@rule[-1].dest_port='53'
set firewall.@rule[-1].target='ACCEPT'
add firewall ipset
set firewall.@ipset[-1].name='uspot'
add_list firewall.@ipset[-1].match='src_mac'
add firewall rule
set firewall.@rule[-1].name='lab-ssh-wan'
set firewall.@rule[-1].src='wan'
set firewall.@rule[-1].proto='tcp'
set firewall.@rule[-1].dest_port='22'
set firewall.@rule[-1].target='ACCEPT'
add firewall rule
set firewall.@rule[-1].name='Allow-captive-DAE'
set firewall.@rule[-1].src='wan'
set firewall.@rule[-1].proto='udp'
set firewall.@rule[-1].src_ip='192.168.77.1'
set firewall.@rule[-1].dest_port='13799'
set firewall.@rule[-1].target='ACCEPT'
B

uci -q delete uhttpd.main.listen_http || true
uci -q delete uhttpd.main.listen_https || true
uci add_list uhttpd.main.listen_http='127.0.0.1:8080'
uci set uhttpd.uspot=uhttpd
uci add_list uhttpd.uspot.listen_http='10.0.0.1:80'
uci set uhttpd.uspot.redirect_https=0
uci set uhttpd.uspot.max_requests=5
uci set uhttpd.uspot.no_dirlists=1
uci set uhttpd.uspot.home=/www-uspot
uci add_list uhttpd.uspot.ucode_prefix='/hotspot=/usr/share/uspot/handler.uc'
uci add_list uhttpd.uspot.ucode_prefix='/cpd=/usr/share/uspot/handler-cpd.uc'
uci set uhttpd.uspot.error_page=/cpd

cat >/etc/config/uspot <<U
config uspot 'captive'
	option auth_mode 'radius'
	option interface 'captive'
	option setname 'uspot'
	option auth_server '192.168.77.1'
	option auth_secret '$SECRET'
	option acct_server '192.168.77.1'
	option acct_secret '$SECRET'
	option counters '1'
	option das_secret '$SECRET'
	option das_port '13799'
	option nasid 'lab-owrt'
	option nasmac '52:54:00:bb:00:01'
U
uci commit
for s in uhttpd ratelimit uspot; do /etc/init.d/$s enable; done
# reboot rather than reloading each service: the LAN address changes under the ssh session
reboot
