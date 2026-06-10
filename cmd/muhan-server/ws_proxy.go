package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/websocket"
)

func startWebSocketProxy(wsListen string, tcpAddr string, stdout io.Writer) {
	fmt.Fprintf(stdout, "websocket proxy listening: ws://%s -> tcp://%s\n", wsListen, tcpAddr)

	// C4: 허용된 Origin 목록 구성
	allowedOrigins := wsAllowedOrigins(wsListen)

	wsHandler := websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		tcpConn, err := net.Dial("tcp", tcpAddr)
		if err != nil {
			log.Printf("WS Proxy dial error: %v", err)
			return
		}
		defer tcpConn.Close()

		errCh := make(chan error, 2)
		go func() {
			_, err := io.Copy(ws, tcpConn)
			errCh <- err
		}()
		go func() {
			_, err := io.Copy(tcpConn, ws)
			errCh <- err
		}()

		<-errCh
	})

	// C4: Origin 검증 래퍼
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && !wsOriginAllowed(origin, allowedOrigins) {
			log.Printf("WS Proxy rejected origin: %s", origin)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		wsHandler.ServeHTTP(w, r)
	})

	httpServer := &http.Server{
		Addr:    wsListen,
		Handler: handler,
	}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("WS Proxy HTTP server error: %v", err)
		}
	}()
}

func wsAllowedOrigins(wsListen string) map[string]bool {
	origins := map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
		"[::1]":     true,
	}
	// wsListen 주소에서 호스트 추출
	host, _, err := net.SplitHostPort(wsListen)
	if err == nil && host != "" {
		origins[host] = true
	}
	// 환경변수로 추가 Origin 허용
	if extra := os.Getenv("MUHAN_WS_ALLOWED_ORIGINS"); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				origins[o] = true
			}
		}
	}
	return origins
}

func wsOriginAllowed(origin string, allowed map[string]bool) bool {
	if origin == "" {
		return true // 비브라우저 클라이언트 허용
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return allowed[host]
}
