#!/bin/sh
set -eu
# Only bridges with the isolated runtime's vb prefix are affected.
iptables -w -N VMBOX-ISOLATED 2>/dev/null || true
iptables -w -C DOCKER-USER -i vb+ -j VMBOX-ISOLATED 2>/dev/null || iptables -w -I DOCKER-USER 1 -i vb+ -j VMBOX-ISOLATED
iptables -w -C INPUT -i vb+ -j REJECT 2>/dev/null || iptables -w -I INPUT 1 -i vb+ -j REJECT
for subnet in 0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 224.0.0.0/4 240.0.0.0/4; do
    iptables -w -C VMBOX-ISOLATED -d "$subnet" -j REJECT 2>/dev/null || iptables -w -A VMBOX-ISOLATED -d "$subnet" -j REJECT
done
ip6tables -w -C INPUT -i vb+ -j REJECT 2>/dev/null || ip6tables -w -I INPUT 1 -i vb+ -j REJECT
ip6tables -w -C FORWARD -i vb+ -j REJECT 2>/dev/null || ip6tables -w -I FORWARD 1 -i vb+ -j REJECT
