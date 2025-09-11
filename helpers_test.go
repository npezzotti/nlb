package main

import (
	"net"
	"testing"
)

func Test_getIpFromAddr(t *testing.T) {
	remoteAddr := &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 5678}
	ip := getIpFromAddr(remoteAddr)
	if ip.String() != "192.168.1.100" {
		t.Errorf("expected 192.168.1.100, got %s", ip)
	}
}

func Test_hashIp(t *testing.T) {
	ip := net.ParseIP("192.168.1.100")
	hash := hashIp(ip)
	expected := int(354263838) // Precomputed hash value for this IP
	if hash != expected {
		t.Errorf("expected %d, got %d", expected, hash)
	}
}
