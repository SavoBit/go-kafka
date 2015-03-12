/* Licensed to the Apache Software Foundation (ASF) under one or more
contributor license agreements.  See the NOTICE file distributed with
this work for additional information regarding copyright ownership.
The ASF licenses this file to You under the Apache License, Version 2.0
(the "License"); you may not use this file except in compliance with
the License.  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License. */

package go_kafka_client

import (
	"fmt"
	"github.com/Shopify/sarama"
	"github.com/stealthly/siesta"
)

// LowLevelClient is a low-level Kafka client that manages broker connections, responsible to fetch metadata and is able
// to handle Fetch and Offset requests.
//TODO not sure that's a good name for this interface
type LowLevelClient interface {
	// This will be called right after connecting to ConsumerCoordinator so this client can initialize itself
	// with bootstrap broker list for example. May return an error to signal this client is unable to work with given configuration.
	Initialize() error

	// This will be called each time the fetch request to Kafka should be issued. Topic, partition and offset are self-explanatory.
	// Should return a slice of Messages and an error if a fetch error occurred.
	// Note that for performance reasons it makes sense to keep open broker connections and reuse them on every fetch call.
	Fetch(topic string, partition int32, offset int64) ([]*Message, error)

	// This will be called when call to Fetch returns an error. As every client has different error mapping we ask here explicitly
	// whether the returned error is an OffsetOutOfRange error. Should return true if it is, false otherwise.
	IsOffsetOutOfRange(error) bool

	// This will be called to handle OffsetOutOfRange error. OffsetTime will be either "smallest" or "largest".
	// Should return a corresponding offset value and an error if it occurred.
	GetAvailableOffset(topic string, partition int32, offsetTime string) (int64, error)

	// This will be called to gracefully shutdown this client.
	Close()
}

// SaramaClient implements LowLevelClient and uses github.com/Shopify/sarama as underlying implementation.
type SaramaClient struct {
	config *ConsumerConfig
	client *sarama.Client
}

// Creates a new SaramaClient using a given ConsumerConfig.
func NewSaramaClient(config *ConsumerConfig) *SaramaClient {
	return &SaramaClient{
		config: config,
	}
}

// Returns a string representation of this SaramaClient.
func (this *SaramaClient) String() string {
	return "Sarama client"
}

// This will be called right after connecting to ConsumerCoordinator so this client can initialize itself
// with bootstrap broker list for example. May return an error to signal this client is unable to work with given configuration.
func (this *SaramaClient) Initialize() error {
	bootstrapBrokers, err := BootstrapBrokers(this.config.Coordinator)
	if err != nil {
		return err
	}

	client, err := sarama.NewClient(this.config.Clientid, bootstrapBrokers, nil)
	if err != nil {
		return err
	}
	this.client = client

	return nil
}

// This will be called each time the fetch request to Kafka should be issued. Topic, partition and offset are self-explanatory.
// Returns slice of Messages and an error if a fetch error occurred.
func (this *SaramaClient) Fetch(topic string, partition int32, offset int64) ([]*Message, error) {
	leader, err := this.client.Leader(topic, partition)
	if err != nil {
		this.client.RefreshTopicMetadata(topic)
		return nil, err
	}

	fetchRequest := new(sarama.FetchRequest)
	fetchRequest.MinBytes = this.config.FetchMinBytes
	fetchRequest.MaxWaitTime = this.config.FetchWaitMaxMs
	Debugf(this, "Adding block: topic=%s, partition=%d, offset=%d, fetchsize=%d", topic, partition, offset, this.config.FetchMessageMaxBytes)
	fetchRequest.AddBlock(topic, partition, offset, this.config.FetchMessageMaxBytes)

	response, err := leader.Fetch(this.config.Clientid, fetchRequest)
	if err != nil {
		this.client.RefreshTopicMetadata(topic)
		return nil, err
	}

	messages := make([]*Message, 0)
	if response != nil {
		Debug(this, "Processing fetch response")
		for topic, partitionAndData := range response.Blocks {
			for partition, data := range partitionAndData {
				switch data.Err {
				case sarama.ErrNoError:
					{
						if len(data.MsgSet.Messages) > 0 {
							this.filterPartitionData(data, offset)
							messages = this.collectMessages(data, topic, partition)
						} else {
							Debugf(this, "No messages in %s:%d at offset %d", topic, partition, offset)
						}
					}
				default:
					{
						this.client.RefreshTopicMetadata(topic)
						return nil, data.Err
					}
				}
			}
		}
	}

	return messages, nil
}

// Checks whether the given error indicates an OffsetOutOfRange error.
func (this *SaramaClient) IsOffsetOutOfRange(err error) bool {
	return err == sarama.ErrOffsetOutOfRange
}

// This will be called to handle OffsetOutOfRange error. OffsetTime will be either "smallest" or "largest".
func (this *SaramaClient) GetAvailableOffset(topic string, partition int32, offsetTime string) (int64, error) {
	time := sarama.LatestOffsets
	if offsetTime == "smallest" {
		time = sarama.EarliestOffset
	}
	offset, err := this.client.GetOffset(topic, partition, time)
	if err != nil {
		return -1, nil
	}

	return offset, nil
}

// Gracefully shuts down this client.
func (this *SaramaClient) Close() {
	this.client.Close()
}

func (this *SaramaClient) filterPartitionData(partitionData *sarama.FetchResponseBlock, requestedOffset int64) {
	lowestCorrectIndex := 0
	for i, v := range partitionData.MsgSet.Messages {
		if v.Offset < requestedOffset {
			lowestCorrectIndex = i + 1
		} else {
			break
		}
	}
	partitionData.MsgSet.Messages = partitionData.MsgSet.Messages[lowestCorrectIndex:]
}

func (this *SaramaClient) collectMessages(partitionData *sarama.FetchResponseBlock, topic string, partition int32) []*Message {
	messages := make([]*Message, 0)

	for _, message := range partitionData.MsgSet.Messages {
		if message.Msg.Set != nil {
			for _, wrapped := range message.Msg.Set.Messages {
				messages = append(messages, &Message{
					Key:       wrapped.Msg.Key,
					Value:     wrapped.Msg.Value,
					Topic:     topic,
					Partition: partition,
					Offset:    wrapped.Offset,
				})
			}
		} else {
			messages = append(messages, &Message{
				Key:       message.Msg.Key,
				Value:     message.Msg.Value,
				Topic:     topic,
				Partition: partition,
				Offset:    message.Offset,
			})
		}
	}

	return messages
}

// SiestaClient implements LowLevelClient and uses github.com/stealthly/siesta as underlying implementation.
type SiestaClient struct {
	config    *ConsumerConfig
	connector siesta.Connector
}

// Creates a new SiestaClient using a given ConsumerConfig.
func NewSiestaClient(config *ConsumerConfig) *SiestaClient {
	return &SiestaClient{
		config: config,
	}
}

// Returns a string representation of this SaramaClient.
func (this *SiestaClient) String() string {
	return "Siesta client"
}

// This will be called right after connecting to ConsumerCoordinator so this client can initialize itself
// with bootstrap broker list for example. May return an error to signal this client is unable to work with given configuration.
func (this *SiestaClient) Initialize() error {
	bootstrapBrokers, err := BootstrapBrokers(this.config.Coordinator)
	if err != nil {
		return err
	}

	connectorConfig := siesta.NewConnectorConfig()
	connectorConfig.BrokerList = bootstrapBrokers
	connectorConfig.ReadTimeout = this.config.SocketTimeout
	connectorConfig.WriteTimeout = this.config.SocketTimeout
	connectorConfig.ConnectTimeout = this.config.SocketTimeout
	connectorConfig.FetchSize = this.config.FetchMessageMaxBytes
	connectorConfig.ClientId = this.config.Clientid

	this.connector, err = siesta.NewDefaultConnector(connectorConfig)
	if err != nil {
		return err
	}

	return nil
}

// This will be called each time the fetch request to Kafka should be issued. Topic, partition and offset are self-explanatory.
// Returns slice of Messages and an error if a fetch error occurred.
func (this *SiestaClient) Fetch(topic string, partition int32, offset int64) ([]*Message, error) {
	Tracef(this, "Fetching %s %d from %d", topic, partition, offset)
	siestaMessages, err := this.connector.Consume(topic, partition, offset)
	if err != nil {
		return nil, err
	}

	//TODO probably it would be good to avoid converting fetchresponse -> siesta.Message -> go_kafka_client.Message but rather fetchresponse -> go_kafka_client.Message
	messages := make([]*Message, len(siestaMessages))
	for i := 0; i < len(siestaMessages); i++ {
		siestaMessage := siestaMessages[i]
		messages[i] = &Message{
			Key:       siestaMessage.Key,
			Value:     siestaMessage.Value,
			Topic:     siestaMessage.Topic,
			Partition: siestaMessage.Partition,
			Offset:    siestaMessage.Offset,
		}
	}

	return messages, nil
}

// Checks whether the given error indicates an OffsetOutOfRange error.
func (this *SiestaClient) IsOffsetOutOfRange(err error) bool {
	return err == siesta.OffsetOutOfRange
}

// This will be called to handle OffsetOutOfRange error. OffsetTime will be either "smallest" or "largest".
func (this *SiestaClient) GetAvailableOffset(topic string, partition int32, offsetTime string) (int64, error) {
	time := siesta.LatestTime
	if offsetTime == "smallest" {
		time = siesta.EarliestTime
	}
	return this.connector.GetAvailableOffset(topic, partition, time)
}

// Gracefully shuts down this client.
func (this *SiestaClient) Close() {
	<-this.connector.Close()
}

// BootstrapBrokers queries the ConsumerCoordinator for all known brokers in the cluster to be used later as a bootstrap list for the LowLevelClient.
func BootstrapBrokers(coordinator ConsumerCoordinator) ([]string, error) {
	bootstrapBrokers := make([]string, 0)
	brokers, err := coordinator.GetAllBrokers()
	if err != nil {
		return nil, err
	}
	for _, broker := range brokers {
		bootstrapBrokers = append(bootstrapBrokers, fmt.Sprintf("%s:%d", broker.Host, broker.Port))
	}

	return bootstrapBrokers, nil
}
