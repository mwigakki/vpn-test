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

	"github.com/songgao/water"
)

// server 的配置文件
type Config struct {
	ListenAddr string `json:listenAddr`
	PSK        string `json:psk` // 预共享密钥
	TunIP      string `json:tunIp`
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

//	AEAD (Authenticated Encryption with Associated Data)
//
// 创建一个 AEAD 加密对象。用于安全通信。AEAD提供了两种重要功能：加密：保护数据机密性认证：确保数据完整性和真实性
// 确保客户端和服务器之间的通信是加密且经过认证的。
func newAEADFromHex(keyHex string) cipher.AEAD {
	key, err := hex.DecodeString(keyHex)
	must(err)
	if len(key) != 32 {
		log.Fatalf("psk must be 32 bytes (hex -> 64 chars), got %d", len(key))
	}
	// 使用AES加密算法创建密码块(cipher.Block)
	block, err := aes.NewCipher(key)
	must(err)
	// 基于AES密码块创建GCM(Galois/Counter Mode)模式的AEAD对象
	aead, err := cipher.NewGCM(block)
	must(err)
	return aead
}

func main() {
	log.SetFlags(log.Ldate | log.Lmicroseconds | log.Lshortfile)
	// 读 config 文件
	cfgfile := flag.String("config", "config.server.json", "config file")
	flag.Parse()
	f, err := os.Open(*cfgfile)
	must(err)
	defer f.Close()
	var cfg Config
	must(json.NewDecoder(f).Decode(&cfg))

	// 创建 AEAD 对象
	aead := newAEADFromHex(cfg.PSK)

	log.Printf("cfg: %s, %s", cfg.ListenAddr, cfg.TunIP)

	// 建立 tun 虚拟网卡，Linux：tun0；macOS：utunX
	iface, err := water.New(water.Config{DeviceType: water.TUN})
	must(err)
	log.Printf("TUN device: %s", iface.Name())
	defer iface.Close()

	// UDP 监听
	udpAddr, err := net.ResolveUDPAddr("udp", cfg.ListenAddr)
	must(err)

	conn, err := net.ListenUDP("udp", udpAddr)
	must(err)
	log.Printf("listening UDP on %s", cfg.ListenAddr)

	// 保持跟踪记录 udp 地址（首次连接时），
	// TODO 拓展多地址
	var clientAddr *net.UDPAddr
	var mu sync.RWMutex

	//TODO 启动服务时自动完成网络配置

	// tun -> udp
	// 接收 tun 的数据（来自客户端），并通过 udp 发送出去
	go func() {
		// 从 tun 网卡读取数据，并发送给 client
		buf := make([]byte, 2000)
		for {
			// 从 tun 网卡读取数据
			n, err := iface.Read(buf)
			if err != nil {
				log.Printf("tun read err: %v", err)
				continue
			}
			mu.RLock()
			ca := clientAddr
			mu.RUnlock()
			if ca == nil {
				log.Printf("no client connected")
				continue
			}
			// 加密然后发送数据给 client
			out, err := encryptPacket(aead, buf[:n])
			if err != nil {
				log.Printf("encrypt err: %v", err)
				continue
			}
			// 加密后的数据直接 UDP 发送到 “对端”
			_, err = conn.WriteToUDP(out, ca)
			if err != nil {
				log.Printf("udp write err: %v", err)
			}
		}
	}()

	// udp -> tun
	buf := make([]byte, 2000)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("udp read err: %v", err)
			continue
		}
		// 记住 udp 对端的地址
		mu.Lock()
		if clientAddr == nil {
			clientAddr = remote
			log.Printf("registered client: %s", clientAddr.String())
		}
		mu.Unlock()
		// 解密数据包
		plain, err := decryptPacket(aead, buf[:n])
		if err != nil {
			log.Printf("decrypt fail from %s: %v", remote.String(), err)
			continue
		}
		// 写入 tun 网卡
		_, err = iface.Write(plain)
		if err != nil {
			log.Printf("tun write err: %v", err)
		}
	}
}

// 数据包加密
func encryptPacket(aead cipher.AEAD, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	// 生成 nonce 随机数，每次加密都生成不同的 nonce
	_, err := io.ReadFull(rand.Reader, nonce)
	if err != nil {
		return nil, err
	}
	// 封装 加密
	ct := aead.Seal(nil, nonce, plaintext, nil)
	// 将 nonce 和 秘文拼接返回，因为接收方需要 nonce 来解密
	out := append(nonce, ct...)
	return out, nil
}

// 数据包解密
func decryptPacket(aead cipher.AEAD, pkt []byte) ([]byte, error) {
	ns := aead.NonceSize()
	if len(pkt) < ns {
		return nil, fmt.Errorf("packet too short")
	}
	nonce := pkt[:ns]
	ct := pkt[ns:]
	// 解密
	plain, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	return plain, nil
}
