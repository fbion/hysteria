package main

import (
	"crypto/tls"
	"crypto/x509"
	"github.com/lucas-clemente/quic-go"
	"github.com/lucas-clemente/quic-go/congestion"
	"github.com/tobyxdd/hysteria/internal/utils"
	hyCongestion "github.com/tobyxdd/hysteria/pkg/congestion"
	"github.com/tobyxdd/hysteria/pkg/core"
	"github.com/tobyxdd/hysteria/pkg/obfs"
	"io/ioutil"
	"log"
	"net"
	"os/user"
)

func relayClient(args []string) {
	var config relayClientConfig
	err := loadConfig(&config, args)
	if err != nil {
		log.Fatalln("Unable to load configuration:", err)
	}
	if err := config.Check(); err != nil {
		log.Fatalln("Configuration error:", err)
	}
	if len(config.Name) == 0 {
		usr, err := user.Current()
		if err == nil {
			config.Name = usr.Name
		}
	}
	log.Printf("Configuration loaded: %+v\n", config)

	tlsConfig := &tls.Config{
		NextProtos: []string{relayTLSProtocol},
		MinVersion: tls.VersionTLS13,
	}
	// Load CA
	if len(config.CustomCAFile) > 0 {
		bs, err := ioutil.ReadFile(config.CustomCAFile)
		if err != nil {
			log.Fatalln("Unable to load CA file:", err)
		}
		cp := x509.NewCertPool()
		if !cp.AppendCertsFromPEM(bs) {
			log.Fatalln("Unable to parse CA file", config.CustomCAFile)
		}
		tlsConfig.RootCAs = cp
	}

	quicConfig := &quic.Config{
		MaxReceiveStreamFlowControlWindow:     config.ReceiveWindowConn,
		MaxReceiveConnectionFlowControlWindow: config.ReceiveWindow,
		KeepAlive:                             true,
	}
	if quicConfig.MaxReceiveStreamFlowControlWindow == 0 {
		quicConfig.MaxReceiveStreamFlowControlWindow = DefaultMaxReceiveStreamFlowControlWindow
	}
	if quicConfig.MaxReceiveConnectionFlowControlWindow == 0 {
		quicConfig.MaxReceiveConnectionFlowControlWindow = DefaultMaxReceiveConnectionFlowControlWindow
	}

	var obfuscator core.Obfuscator
	if len(config.Obfs) > 0 {
		obfuscator = obfs.XORObfuscator(config.Obfs)
	}

	client, err := core.NewClient(config.ServerAddr, config.Name, "", tlsConfig, quicConfig,
		uint64(config.UpMbps)*mbpsToBps, uint64(config.DownMbps)*mbpsToBps,
		func(refBPS uint64) congestion.SendAlgorithmWithDebugInfos {
			return hyCongestion.NewBrutalSender(congestion.ByteCount(refBPS))
		}, obfuscator)
	if err != nil {
		log.Fatalln("Client initialization failed:", err)
	}
	defer client.Close()
	log.Println("Connected to", config.ServerAddr)

	listener, err := net.Listen("tcp", config.ListenAddr)
	if err != nil {
		log.Fatalln("TCP listen failed:", err)
	}
	defer listener.Close()
	log.Println("TCP listening on", listener.Addr().String())

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalln("TCP accept failed:", err)
		}
		go relayClientHandleConn(conn, client)
	}
}

func relayClientHandleConn(conn net.Conn, client core.Client) {
	log.Println("New connection", conn.RemoteAddr().String())
	var closeErr error
	defer func() {
		_ = conn.Close()
		log.Println("Connection", conn.RemoteAddr().String(), "closed", closeErr)
	}()
	rwc, err := client.Dial(false, "")
	if err != nil {
		closeErr = err
		return
	}
	defer rwc.Close()
	closeErr = utils.PipePair(conn, rwc, nil, nil)
}
