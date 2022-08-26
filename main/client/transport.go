package main

import (
	"net"

	"gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/webtunnel/transport/httpupgrade"

	"gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/webtunnel/transport/tls"
)

type ClientConfig struct {
	RemoteAddress string

	Path          string
	TLSKind       string
	TLSServerName string
}

type Transport struct {
	config *ClientConfig
}

func NewWebTunnelClientTransport(config *ClientConfig) (Transport, error) {
	return Transport{config: config}, nil
}

func (t Transport) Dial() (net.Conn, error) {
	var conn net.Conn
	if tcpConn, err := net.Dial("tcp", t.config.RemoteAddress); err != nil {
		return nil, err
	} else {
		conn = tcpConn
	}
	if t.config.TLSKind != "" {
		conf := &tls.Config{ServerName: t.config.TLSServerName}
		if tlsTransport, err := tls.NewTLSTransport(conf); err != nil {
			return nil, err
		} else {
			if tlsConn, err := tlsTransport.Client(conn); err != nil {
				return nil, err
			} else {
				conn = tlsConn
			}
		}
	}
	upgradeConfig := httpupgrade.Config{Path: t.config.Path, Host: t.config.TLSServerName}
	if httpupgradeTransport, err := httpupgrade.NewHTTPUpgradeTransport(&upgradeConfig); err != nil {
		return nil, err
	} else {
		if httpUpgradeConn, err := httpupgradeTransport.Client(conn); err != nil {
			return nil, err
		} else {
			conn = httpUpgradeConn
		}
	}
	return conn, nil
}
