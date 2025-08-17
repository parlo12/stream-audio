// ===============
// File: mqtt.go
// ===============
package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var mqttClient mqtt.Client

// InitMQTT initializes and connects the MQTT client.
func InitMQTT() {
	// Use tcp:// for your VPC broker (no TLS). You’ll override this via env anyway.
	// Example .env: MQTT_BROKER=tcp://10.116.0.8:1883
	broker := getEnv("MQTT_BROKER", "tcp://mqtt-broker:1883")
	if broker == "" {
		log.Println("⚠️ MQTT_BROKER not set; starting without MQTT")
		return
	}
	clientID := fmt.Sprintf("svc-content-%d", time.Now().UnixNano())
	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(clientID).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second).
		SetConnectTimeout(10 * time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	// 👇 Add username/password support (reads from .env)
	if u := getEnv("MQTT_USERNAME", ""); u != "" {
		opts.SetUsername(u)
	}
	if p := getEnv("MQTT_PASSWORD", ""); p != "" {
		opts.SetPassword(p)
	}

	// Only set TLS if you’re actually using tls:// or ssl://
	if strings.HasPrefix(broker, "tls://") || strings.HasPrefix(broker, "ssl://") {
		opts.SetTLSConfig(&tls.Config{
			// For real certs, keep this false.
			// Set to true ONLY if you knowingly use self-signed certs.
			InsecureSkipVerify: false,
		})
	}

	opts.OnConnect = func(c mqtt.Client) {
		log.Printf("✅ MQTT connected to %s", broker)
	}
	opts.OnConnectionLost = func(c mqtt.Client, err error) {
		log.Printf("⚠️ MQTT connection lost: %v", err)
	}

	mqttClient = mqtt.NewClient(opts)
	token := mqttClient.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		// log.Fatalf("❌ MQTT connection error: %v", err)
		log.Printf("⚠️ MQTT connect failed: %v (broker=%s). Continuing without MQTT.", err, broker)
		return
	}
	log.Println("✅ MQTT connected to broker at", broker)
}

// PublishEvent publishes a JSON payload to the specified MQTT topic.
func PublishEvent(topic string, payload []byte) {
	tok := mqttClient.Publish(topic, 1, false, payload)
	tok.WaitTimeout(5 * time.Second)
	if err := tok.Error(); err != nil {
		log.Printf("⚠️ MQTT publish to %s failed: %v", topic, err)
	}
}
