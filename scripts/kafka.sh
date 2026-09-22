#!/bin/zsh
# kafka.sh [up|down] — the Kafka containers the tagged suites read.
#
# The tagged tests skip where nothing is listening, so this is what turns a
# skip into a run: `make kafka-up` before `make conformance`.
#
# Two brokers, and the second one needs saying. A schema registry runs in a
# container and has to reach a broker by name, which the default bridge
# network cannot do — it has no DNS — and both of this project's other brokers
# advertise themselves as localhost, which inside a container means the
# container itself. So the registry gets a broker of its own on a user-defined
# network, advertising two listeners: one the registry reaches by name, and
# one this machine reaches on a published port (ADR-0101).
#
# What this does not start is the SASL and TLS broker. Its certificates and
# JAAS file are the owner's and live outside this repository, so a script here
# could only guess at them; the tests that need it skip without it.
#
# Everything is named and idempotent. Starting what is already running is not
# an error, and nothing is removed unless "down" is asked for.

set -u
cmd=${1:-up}

network=ikigai-sr
broker=ikigai-kafka
srbroker=ikigai-kafka-sr
registry=ikigai-schema-registry

kafka_image=apache/kafka:latest
registry_image=confluentinc/cp-schema-registry:7.7.1

# running reports whether a container of this name is up.
running() { [ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = true ]; }

# gone removes a container whether it is running or merely lying about.
gone() { docker rm -f "$1" >/dev/null 2>&1 || true; }

if [ "$cmd" = down ]; then
  for c in $registry $srbroker $broker; do
    gone $c
    echo "removed $c"
  done
  docker network rm $network >/dev/null 2>&1 || true
  echo "removed the $network network"
  exit 0
fi

if [ "$cmd" != up ]; then
  echo "usage: kafka.sh [up|down]" >&2
  exit 1
fi

# The plain broker, which most of the tagged tests read.
if running $broker; then
  echo "$broker is already up"
else
  gone $broker
  docker run -d --name $broker -p 59092:9092 \
    -e KAFKA_NODE_ID=1 \
    -e KAFKA_PROCESS_ROLES=broker,controller \
    -e KAFKA_LISTENERS=PLAINTEXT://:9092,CONTROLLER://:9093 \
    -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:59092 \
    -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
    -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
    -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 \
    -e KAFKA_INTER_BROKER_LISTENER_NAME=PLAINTEXT \
    -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
    -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 \
    -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
    -e KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 \
    $kafka_image >/dev/null || exit 1
  echo "started $broker on 59092"
fi

# A network the registry and its broker can find each other on by name.
docker network create $network >/dev/null 2>&1 || true

# The registry's own broker: INTERNAL is what the registry dials by name,
# HOST is what this machine dials on 59093.
if running $srbroker; then
  echo "$srbroker is already up"
else
  gone $srbroker
  docker run -d --name $srbroker --network $network -p 59093:9094 \
    -e KAFKA_NODE_ID=1 \
    -e KAFKA_PROCESS_ROLES=broker,controller \
    -e KAFKA_LISTENERS=INTERNAL://:9092,HOST://:9094,CONTROLLER://:9093 \
    -e KAFKA_ADVERTISED_LISTENERS=INTERNAL://$srbroker:9092,HOST://localhost:59093 \
    -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,INTERNAL:PLAINTEXT,HOST:PLAINTEXT \
    -e KAFKA_INTER_BROKER_LISTENER_NAME=INTERNAL \
    -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
    -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 \
    -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
    -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 \
    -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
    -e KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 \
    $kafka_image >/dev/null || exit 1
  echo "started $srbroker on 59093"
fi

# ready waits for a broker to say it has started, and for the port this
# machine reads it on to answer.
#
# It does not ask the broker to list topics. A client inside the container is
# told where the broker advertises itself, which is an address that means
# nothing inside the container — the same trap the registry needed its own
# network to avoid (ADR-0101), met again from the other side.
ready() {
  local name=$1 port=$2 i
  for i in $(seq 1 90); do
    if docker logs $name 2>&1 | grep -q "Kafka Server started" &&
       nc -z -G 2 127.0.0.1 $port >/dev/null 2>&1; then
      echo "$name answers on $port"
      return 0
    fi
    sleep 2
  done
  echo "$name did not start within three minutes" >&2
  docker logs --tail 20 $name >&2
  return 1
}

ready $broker 59092 || exit 1
ready $srbroker 59093 || exit 1

# The registry, which needs its broker answering before it will start.
if running $registry; then
  echo "$registry is already up"
else
  gone $registry
  docker run -d --name $registry --network $network -p 58081:8081 \
    -e SCHEMA_REGISTRY_HOST_NAME=$registry \
    -e SCHEMA_REGISTRY_LISTENERS=http://0.0.0.0:8081 \
    -e SCHEMA_REGISTRY_KAFKASTORE_BOOTSTRAP_SERVERS=PLAINTEXT://$srbroker:9092 \
    $registry_image >/dev/null || exit 1
  echo "started $registry on 58081"
fi

for i in $(seq 1 60); do
  if curl -sf http://127.0.0.1:58081/subjects >/dev/null 2>&1; then
    echo "$registry answers"
    exit 0
  fi
  sleep 2
done
echo "$registry did not answer within two minutes" >&2
docker logs --tail 20 $registry >&2
exit 1
