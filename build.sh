#!/bin/bash

killall game >/dev/null
killall gate >/dev/null

rm -rf logs/*

echo "stoping..."

go build -o game example/demo05/game/*.go
go build -o gate example/demo05/gate/*.go
go build -o client example/demo05/test/stress_client.go

# PPROF_PORT=9008 ./game GAME :9001 >logs/game1.log 2>&1 &
# PPROF_PORT=9010 ./game GAME :9002 >logs/game2.log 2>&1 &
# PPROF_PORT=9011 ./game GAME :9003 >logs/game3.log 2>&1 &
# ./gate GATE :8080 >logs/gate.log 2>&1 &
