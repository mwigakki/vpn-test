package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/labulakalia/water"
)

// client windows 版本
// 在 windows 创建一个 TUN 网卡
// 把收到的数据用简单对称加密（比如 AES）做封装
// 使用 UDP 把数据发给远端,(类似于使用UDP 4层协议实现 2层协议，)
// 远端解密后写回它自己的 TUN 网卡

type Config struct {
	ServerAddr string `json:"serverAddr"` // e.g. "1.2.3.4:11945"
	PSK        string `json:"psk"`        // hex encoded 32 bytes
	TunIP      string `json:"tunIp"`      // e.g. "10.8.0.2/24"
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func newAEADFromHex(keyHex string) cipher.AEAD {
	key, err := hex.DecodeString(keyHex)
	must(err)
	if len(key) != 32 {
		log.Fatalf("psk must be 32 bytes (hex -> 64 chars), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	must(err)
	aead, err := cipher.NewGCM(block)
	must(err)
	return aead
}

func main() {
	cfgFile := flag.String("config", "config.client.json", "config file")
	flag.Parse()

	f, err := os.Open(*cfgFile)
	must(err)
	defer f.Close()
	var cfg Config
	must(json.NewDecoder(f).Decode(&cfg))
	log.Printf("config serverAddr:%s", cfg.ServerAddr)

	aead := newAEADFromHex(cfg.PSK)

	iface, err := water.New(water.Config{DeviceType: water.TUN})
	must(err)
	log.Printf("TUN device: %s", iface.Name())
	defer iface.Close()

	serverAddr, err := net.ResolveUDPAddr("udp", cfg.ServerAddr)
	must(err)
	conn, err := net.DialUDP("udp", nil, serverAddr)
	must(err)
	log.Printf("dialed server %s", serverAddr.String())

	var wg sync.WaitGroup
	wg.Add(2)
	// 客户端需要配置路由规则使流量 发给 tun 网卡
	// tun -> udp
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := iface.Read(buf)
			if err != nil {
				log.Printf("tun read err: %v", err)
				continue
			}
			enc, err := encryptPacket(aead, buf[:n])
			if err != nil {
				log.Printf("encrypt err: %v", err)
				continue
			}
			_, err = conn.Write(enc)
			if err != nil {
				log.Printf("udp write err: %v", err)
			}
		}
	}()

	// udp -> tun
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				log.Printf("udp read err: %v", err)
				continue
			}
			plain, err := decryptPacket(aead, buf[:n])
			if err != nil {
				log.Printf("decrypt fail: %v", err)
				continue
			}
			_, err = iface.Write(plain)
			if err != nil {
				log.Printf("tun write err: %v", err)
			}
		}
	}()
	wg.Wait()
}

func encryptPacket(aead cipher.AEAD, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	_, err := io.ReadFull(rand.Reader, nonce)
	if err != nil {
		return nil, err
	}
	ct := aead.Seal(nil, nonce, plaintext, nil)
	out := append(nonce, ct...)
	return out, nil
}

func decryptPacket(aead cipher.AEAD, pkt []byte) ([]byte, error) {
	ns := aead.NonceSize()
	if len(pkt) < ns {
		return nil, fmt.Errorf("packet too short")
	}
	nonce := pkt[:ns]
	ct := pkt[ns:]
	plain, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	return plain, nil
}
