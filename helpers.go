package main

import (
	"hash/fnv"
	"net"
	"strings"
)

// getIpFromAddr extracts the IP address from the connection.
func getIpFromAddr(addr net.Addr) net.IP {
	ip := strings.Split(addr.String(), ":")[0]
	return net.ParseIP(ip)
}

// hashIp hashes the IP address to a consistent integer.
func hashIp(ip net.IP) int {
	h := fnv.New32a()
	h.Write(ip)
	hash := h.Sum32()
	return int(hash)
}

