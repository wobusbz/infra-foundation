#!/bin/bash

protoc --plugin=protoc-gen-messageid=./protoc-gen-messageid --messageid_out=. --go_out=. *.proto
