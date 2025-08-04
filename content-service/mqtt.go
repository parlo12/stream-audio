// ===============
// File: mqtt.go
// ===============
package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var mqttClient mqtt.Client

// InitMQTT initializes and connects the MQTT client.
func InitMQTT() {
	broker := getEnv("MQTT_BROKER", "tls://mqtt-broker:8883")
	clientID := fmt.Sprintf("svc-content-%d", time.Now().UnixNano())

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(clientID).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second).
		SetTLSConfig(&tls.Config{InsecureSkipVerify: true})

	mqttClient = mqtt.NewClient(opts)
	token := mqttClient.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		log.Fatalf("❌ MQTT connection error: %v", err)
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
