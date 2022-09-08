package main

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/elastic/go-elasticsearch/v8"
)

type ElasticParams struct {
	url      string
	user     string
	password string
	index    string
}

// Connect accepts ElasticParams which describe how to connect to ES.
// Returns a client connected to the desired ES Cluster.
func Connect(es ElasticParams) (*elasticsearch.Client, error) {
	log.Infof("Connecting to ES - %s", es.url)
	esc := elasticsearch.Config{
		Username:  es.user,
		Password:  es.password,
		Addresses: []string{es.url},
	}
	ec, err := elasticsearch.NewClient(esc)
	if err != nil {
		return nil, fmt.Errorf("Error connecting to ES")
	}
	return ec, nil
}
