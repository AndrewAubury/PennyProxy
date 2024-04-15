package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/fatih/color"
)

type Config struct {
	ServerAddress string        `json:"serverAddress"`
	ServerTimeout time.Duration `json:"serverTimeout"`
	ListenAddress string        `json:"listenAddress"`
	ListenPort    string        `json:"listenPort"`
}

var serverIsHealthy bool
var lastChange time.Time

func readConfig(path string) (*Config, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config Config
	err = json.Unmarshal(file, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// transferAndLogData transfers data between src and dst, logging the data
func transferAndLogData(dst io.Writer, src io.Reader, connectionType string) {
	buffer := make([]byte, 32*1024) // buffer size 32KB
	for {
		n, err := src.Read(buffer)
		if n > 0 {
			data := buffer[:n]
			// Log the data
			//dst2 := make([]byte, hex.DecodedLen(len(data)))
			//hex.Decode(dst2, data)
			if len(data) < 200 {
				if false {
					fmt.Printf("%s data: %x\n", connectionType, data)
				}

			}
			_, writeErr := dst.Write(data)
			if writeErr != nil {
				fmt.Println("Write error:", writeErr)
				break
			}
		}
		if err != nil {
			if err != io.EOF {
				fmt.Println("Read error:", err)
			}
			break
		}
	}
}

// checkServer attempts to establish a TCP connection to check the server's health
func checkServer(address string, timeoutSec time.Duration) bool {
	timeout := timeoutSec * time.Second
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		//fmt.Println("Server is down:", err)
		return false
	}
	defer conn.Close()
	//fmt.Println("Server is up and running")
	return true
}

// MinecraftPacketHeader creates a packet header for Minecraft server
func MinecraftPacketHeader(packetID int, data []byte) []byte {
	var packet []byte
	// packet = append(packet, byte(len(data)+1)) // Length of packetID + data
	// packet = append(packet, byte(packetID))    // Packet ID

	prefix := []byte{0x92, 0x01, 0x00, 0x8f, 0x01}

	packet = append(packet, prefix...)
	packet = append(packet, data...) // Actual data
	return packet
}

// KickPlayer sends a disconnect packet to the player
func KickPlayer(conn net.Conn) error {
	// Disconnect message JSON
	message := `{"text":"Please reconnect later!"}`
	msgJSON, _ := json.Marshal(message)
	packet := MinecraftPacketHeader(0x1A, msgJSON) // Disconnect Play Packet ID
	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("failed to send kick packet: %v", err)
	}
	return nil
}

// WriteVarInt writes a Minecraft VarInt to a slice of bytes
func WriteVarInt(x int) []byte {
	var buf [5]byte
	n := 0
	for x >= 0x80 {
		buf[n] = byte(x) | 0x80
		x >>= 7
		n++
	}
	buf[n] = byte(x)
	return buf[:n+1]
}

// SendPacket sends a Minecraft protocol packet to the connection
func SendPacket(conn net.Conn, packetID int, data []byte) error {

	extraData := []byte{0x8F, 0x01}
	length := 1 + len(data) + len(extraData)
	lengthBytes := WriteVarInt(length)

	packet := append(lengthBytes, WriteVarInt(packetID)...)

	packet = append(packet, extraData...)

	packet = append(packet, data...)

	_, err := conn.Write(packet)
	fmt.Printf("%s data: %x\n", "Sending Packet Local", packet)
	return err
}

// SetServerList sends a response for the server list query
func SetServerList(conn net.Conn) error {
	serverList := `{"version":{"name":"Paper 1.20.4","protocol":765},"enforcesSecureChat":true,"description":"A Minecraft Faker.","players":{"max":20,"online":0}}`

	return SendPacket(conn, 0x00, []byte(serverList)) // Assuming 0x00 is the packet ID for "Server List Response"
}

func handleOfflineConnection(conn net.Conn) {
	// Handle the connection
	if err := SetServerList(conn); err != nil {
		fmt.Println(err)
	}

	// if err := KickPlayer(conn); err != nil {
	// 	fmt.Println(err)
	// }

	conn.Close()
}

// handleClient handles client connections, proxying data between the client and the server
func handleClient(clientConn net.Conn, serverAddress string) {
	serverConn, err := net.Dial("tcp", serverAddress)
	if err != nil {
		fmt.Println("Failed to connect to server:", err)
		clientConn.Close()
		return
	}
	fmt.Printf("New connection being handled from %s\n", clientConn.RemoteAddr())

	// Use goroutines to handle bi-directional data transfer and logging
	go transferAndLogData(serverConn, clientConn, "Client to Server")
	go transferAndLogData(clientConn, serverConn, "Server to Client")
}

func startProxy(listenAddress string, listenPort string, serverAddress string) {
	listener, err := net.Listen("tcp", listenAddress+":"+listenPort)
	if err != nil {
		fmt.Printf("Failed to listen on port %s: %v\n", listenPort, err)
		return
	}
	fmt.Printf("Proxy server listening on %s:%s\n", listenAddress, listenPort)
	for {
		clientConn, err := listener.Accept()
		if err != nil {
			fmt.Println("Failed to accept connection:", err)
			continue
		}
		if serverIsHealthy {
			go handleClient(clientConn, serverAddress)
		} else {
			go handleOfflineConnection(clientConn)
		}

	}
}

func startHealthCheck(serverAddress string, timeout time.Duration, frequecy time.Duration) {
	// Continuously check server health
	for {
		currentStatus := checkServer(serverAddress, timeout)
		if currentStatus != serverIsHealthy {
			serverIsHealthy = currentStatus
			currentTime := time.Now()
			if serverIsHealthy {
				color.Green("Server health changed to %t at %s, time since last change: %s\n",
					serverIsHealthy, currentTime.Format(time.RFC3339), currentTime.Sub(lastChange))
			} else {
				color.Red("Server health changed to %t at %s, time since last change: %s\n",
					serverIsHealthy, currentTime.Format(time.RFC3339), currentTime.Sub(lastChange))
			}

			lastChange = currentTime
		}
		time.Sleep(frequecy * time.Second)
	}
}

func main() {
	config, err := readConfig("config.json")

	if err != nil {
		fmt.Printf("Error reading config file: %s\n", err)
		os.Exit(1)
	}
	lastChange = time.Now()
	go startProxy(config.ListenAddress, config.ListenPort, config.ServerAddress)
	go startHealthCheck(config.ServerAddress, config.ServerTimeout, 1)

	fmt.Println("Press CTRL+C to exit")
	for {
	} // Loop forever

}
