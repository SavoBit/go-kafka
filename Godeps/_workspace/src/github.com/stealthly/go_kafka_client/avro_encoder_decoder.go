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
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/stealthly/go-avro"
)

var magic_bytes = []byte{0}

type KafkaAvroEncoder struct {
	primitiveSchemas map[string]avro.Schema
	schemaRegistry   SchemaRegistryClient
}

func NewKafkaAvroEncoder(url string) *KafkaAvroEncoder {
	primitiveSchemas := make(map[string]avro.Schema)
	primitiveSchemas["Null"] = createPrimitiveSchema("null")
	primitiveSchemas["Boolean"] = createPrimitiveSchema("boolean")
	primitiveSchemas["Int"] = createPrimitiveSchema("int")
	primitiveSchemas["Long"] = createPrimitiveSchema("long")
	primitiveSchemas["Float"] = createPrimitiveSchema("float")
	primitiveSchemas["Double"] = createPrimitiveSchema("double")
	primitiveSchemas["String"] = createPrimitiveSchema("string")
	primitiveSchemas["Bytes"] = createPrimitiveSchema("bytes")

	return &KafkaAvroEncoder{
		schemaRegistry:   NewCachedSchemaRegistryClient(url),
		primitiveSchemas: primitiveSchemas,
	}
}

func (this *KafkaAvroEncoder) Encode(obj interface{}) ([]byte, error) {
	switch record := obj.(type) {
	case *avro.GenericRecord:
		{
			if record == nil {
				return nil, nil
			}

			subject := record.Schema().GetName() + "-value"
			schema := this.getSchema(obj)
			id, err := this.schemaRegistry.Register(subject, schema)
			if err != nil {
				return nil, err
			}

			buffer := &bytes.Buffer{}
			buffer.Write(magic_bytes)
			idSlice := make([]byte, 4)
			binary.BigEndian.PutUint32(idSlice, uint32(id))
			buffer.Write(idSlice)

			enc := avro.NewBinaryEncoder(buffer)
			writer := avro.NewGenericDatumWriter()
			writer.SetSchema(schema)
			writer.Write(obj, enc)

			return buffer.Bytes(), nil
		}
	default:
		return nil, errors.New("*GenericRecord is expected")
	}
}

func (this *KafkaAvroEncoder) getSchema(obj interface{}) avro.Schema {
	if obj == nil {
		return this.primitiveSchemas["Null"]
	}

	switch t := obj.(type) {
	case bool:
		return this.primitiveSchemas["Boolean"]
	case int32:
		return this.primitiveSchemas["Int"]
	case int64:
		return this.primitiveSchemas["Long"]
	case float32:
		return this.primitiveSchemas["Float"]
	case float64:
		return this.primitiveSchemas["Double"]
	case string:
		return this.primitiveSchemas["String"]
	case []byte:
		return this.primitiveSchemas["Bytes"]
	case *avro.GenericRecord:
		return t.Schema()
	default:
		panic("Unsupported Avro type. Supported types are nil, bool, int32, int64, float32, float64, string, []byte and *GenericRecord")
	}
}

func createPrimitiveSchema(schemaType string) avro.Schema {
	schema, err := avro.ParseSchema(fmt.Sprintf(`{"type" : "%s" }`, schemaType))
	if err != nil {
		panic(err)
	}

	return schema
}

type KafkaAvroDecoder struct {
	schemaRegistry SchemaRegistryClient
}

func NewKafkaAvroDecoder(url string) *KafkaAvroDecoder {
	return &KafkaAvroDecoder{
		schemaRegistry: NewCachedSchemaRegistryClient(url),
	}
}

func (this *KafkaAvroDecoder) Decode(bytes []byte) (interface{}, error) {
	if bytes == nil {
		return nil, nil
	} else {
		if bytes[0] != 0 {
			return nil, errors.New("Unknown magic byte!")
		}
		id := int32(binary.BigEndian.Uint32(bytes[1:]))
		schema, err := this.schemaRegistry.GetByID(id)
		if err != nil {
			return nil, err
		}

		if schema.Type() == avro.Bytes {
			return bytes[5:], nil
		} else {
			reader := avro.NewGenericDatumReader()
			reader.SetSchema(schema)
			value, err := reader.Read(nil, avro.NewBinaryDecoder(bytes[5:]))
			if err != nil {
				return nil, err
			}

			return value, nil
		}
	}
}
