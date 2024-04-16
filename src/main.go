package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	mcnet "github.com/Tnze/go-mc/net"
	"github.com/Tnze/go-mc/net/packet"

	"github.com/fatih/color"
)

type PterodactylOptions struct {
	PterodactylURL    string `json:"url"`
	PterodactylKey    string `json:"apikey"`
	PterodactylServer string `json:"server"`
}

type Config struct {
	ServerAddress string             `json:"serverAddress"`
	Pterodactyl   PterodactylOptions `json:"pterodactyl"`
	ServerTimeout time.Duration      `json:"serverTimeout"`
	ListenAddress string             `json:"listenAddress"`
	ListenPort    string             `json:"listenPort"`
}

var pterodactylOptions PterodactylOptions
var serverIsHealthy bool
var serverBootRequested bool
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

// KickPlayer kicks a player if they try to join, telling them to connect later
func KickPlayerLib(conn mcnet.Conn) error {

	jsonReason := fmt.Sprintf(`{"text":"%s", "color":"red"}`, "A request to turn on the server has been sent, please rejoin in a min")
	jsonReasonSub := fmt.Sprintf(`{"text":"%s", "color":"red"}`, "The server is booting, please rejoin in a min")

	if serverBootRequested {
		// Prepare the Disconnect packet
		disconnectPacket := packet.Marshal(
			0x00, // Packet ID for Disconnect in Login state
			packet.String(jsonReasonSub),
		)
		if err := conn.WritePacket(disconnectPacket); err != nil {
			return fmt.Errorf("failed to send kick packet: %v", err)
		}
	} else {
		// Prepare the Disconnect packet
		disconnectPacket := packet.Marshal(
			0x00, // Packet ID for Disconnect in Login state
			packet.String(jsonReason),
		)
		if err := conn.WritePacket(disconnectPacket); err != nil {
			return fmt.Errorf("failed to send kick packet: %v", err)
		}
		color.Yellow("Sending server boot request")
		if err2 := startServer(pterodactylOptions.PterodactylKey, pterodactylOptions.PterodactylServer); err2 != nil {
			color.Red("Server start API call failed: ", err2)
			return nil
		}
		color.Green("Server start API call successed")
		serverBootRequested = true

	}

	return nil
}

// SetServerList sets the server list information when players ping the server
func SetServerListLib(conn mcnet.Conn) error {
	// Example response for server list
	serverList := `{
		"version": {
			"name": "1.20.4",
			"protocol": 765
		},
		"players": {
			"max": 100,
			"online": 0
		},
		"description": {
			"text": "Join to start Server",
			"color":"red"
		}
	}`
	serverListPacket := packet.Marshal(
		0x00, // The packet ID for "Server List Response"
		packet.String(serverList),
	)

	if err := conn.WritePacket(serverListPacket); err != nil {
		return fmt.Errorf("failed to set server list: %v", err)
	}

	return nil
}

func handleLocalPacket(conn mcnet.Conn, p packet.Packet) {

	//fmt.Printf("p: %v\n", p)
	if p.Data[len(p.Data)-1] == 0x02 {
		color.Cyan("Server join recived from %s", conn.Socket.RemoteAddr())
		fmt.Printf("p: %v\n", p)
		KickPlayerLib(conn)
	} else {
		color.Cyan("Server list ping recived from %s", conn.Socket.RemoteAddr())
		SetServerListLib(conn)
	}

}

func handleOfflineClient(conn mcnet.Conn) {

	var p packet.Packet
	err := conn.ReadPacket(&p)
	if err != nil {
		fmt.Println(err)
		return
	}
	handleLocalPacket(conn, p)

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
			go handleOfflineClient(*mcnet.WrapConn(clientConn))
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
				serverBootRequested = false
			} else {
				color.Red("Server health changed to %t at %s, time since last change: %s\n",
					serverIsHealthy, currentTime.Format(time.RFC3339), currentTime.Sub(lastChange))
			}

			lastChange = currentTime
		}
		time.Sleep(frequecy * time.Second)
	}
}

// startServer sends a POST request to Pterodactyl Panel to start a server.
func startServer(apiToken string, serverIdentifier string) error {
	// Set up the API endpoint. Replace "your-panel-url.com" with your actual panel URL.
	url := fmt.Sprintf("%s/api/client/servers/%s/power", pterodactylOptions.PterodactylURL, serverIdentifier)

	// Create the JSON body for the request
	var jsonData = []byte(`{"signal":"start"}`)

	// Create a new request using http
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error creating request: %v", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to server: %v", err)
	}
	defer resp.Body.Close()

	// Read and print the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %v", err)
	}

	if false {
		fmt.Printf("Response from server: %s\n", string(body))
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to start server, status code: %d", resp.StatusCode)
	}

	return nil
}

func main() {
	config, err := readConfig("config.json")

	if err != nil {
		fmt.Printf("Error reading config file: %s\n", err)
		os.Exit(1)
	}
	lastChange = time.Now()
	pterodactylOptions = config.Pterodactyl
	go startProxy(config.ListenAddress, config.ListenPort, config.ServerAddress)
	go startHealthCheck(config.ServerAddress, config.ServerTimeout, 1)

	fmt.Println("Press CTRL+C to exit")
	for {
	} // Loop forever

}
