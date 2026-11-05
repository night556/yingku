package main

import (
	"errors"
	"net"
	"strconv"
)

func listenService(address string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("监听地址格式不正确，例如 0.0.0.0:8787")
	}
	n, err := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || n < 0 || n > 65535 || (host != "" && host != "localhost" && ip == nil) {
		return nil, errors.New("请使用有效的 IP 地址和端口，例如 0.0.0.0:8787")
	}
	network := "tcp"
	if ip != nil && ip.To4() != nil {
		network = "tcp4"
	}
	return net.Listen(network, address)
}

func browserServiceURL(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
		if ip != nil && ip.To4() == nil {
			host = "::1"
		}
	}
	return "http://" + net.JoinHostPort(host, port)
}

func listenerPort(address string) int {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

func listenerAllowsLAN(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil || listenerPort(address) < 1 || host == "localhost" {
		return false
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && !ip.IsLoopback()
}

func lanServiceURLs(address string) []string {
	urls := []string{}
	if !listenerAllowsLAN(address) {
		return urls
	}
	host, port, _ := net.SplitHostPort(address)
	bound := net.ParseIP(host)
	for _, ip := range lanIPs() {
		if host == "" || bound.IsUnspecified() || bound.Equal(ip) {
			urls = append(urls, "http://"+net.JoinHostPort(ip.String(), port))
		}
	}
	return urls
}
