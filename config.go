package main

import (
	"encoding/json"
	"os"
)

type WorkerConfig struct {
	BlueBox BlueBoxConfig `json:"bluebox"`
}

type BlueBoxConfig struct {
	CustomerId string `json:"customer_id"`
	ApiKey     string `json:"api_key"`
	LocationId string `json:"location_id"`
	TemplateId string `json:"image_id"`
	ProductId  string `json:"product_id"`
}

func ConfigFromFile(fileName string) (c WorkerConfig, err error) {
	file, err := os.Open(fileName)
	if err != nil {
		return
	}

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&c)
	return
}
