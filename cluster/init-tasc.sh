#!/bin/bash

IP=`dig +short myip.opendns.com @resolver1.opendns.com`

# A helper function that takes a space separated list and generates a string
# that parses as a YAML list.
gen_yml_list() {
  IFS=' ' read -r -a ARR <<< $1
  RESULT=""

  for IP in "${ARR[@]}"; do
    RESULT=$"$RESULT        - $IP\n"
  done

  echo -e "$RESULT"
}

# Create the AWS access key infrastructure.
mkdir -p ~/.aws
echo -e "[default]\nregion = us-east-1" > ~/.aws/config
echo -e "[default]\naws_access_key_id = $AWS_ACCESS_KEY_ID\naws_secret_access_key = $AWS_SECRET_ACCESS_KEY" > ~/.aws/credentials

if [[ "$ROLE" = "lb" ]]; then
  mkdir -p /root/.kube
fi

cd $GOPATH/src/github.com/saurav-c/aftsi

git pull 
git checkout ${BRANCH}
git pull origin ${BRANCH}

# Wait for the config file to be passed in.
while [[ ! -f $GOPATH/src/github.com/saurav-c/aftsi/config/tasc-config.yml ]]; do
  sleep 1
done

# Generate the YML config file.
echo "ipAddress: $IP" >> config/tasc-config.yml
echo "keyRouterIP: $KEY_ROUTER" >> config/tasc-config.yml
echo "monitorIP: $MONITOR" >> config/tasc-config.yml
LST=$(gen_yml_list "$NODE_IPS")
echo "nodeIPs:" >> config/tasc-config.yml
echo "$LST" >> config/tasc-config.yml

# Start the process.
if [[ "$ROLE" = "tasc" ]]; then
  cd $TASC_HOME/cmd/aftsi
  go build
  ./aftsi
elif [[ "$ROLE" = "keynode" ]]; then
  cd $TASC_HOME/cmd/keynode
  go build
  ./keynode
elif [[ "$ROLE" = "keyrouter" ]]; then
  cd $TASC_HOME/cmd/routing
  go build
  ./routing -mode key
elif [[ "$ROLE" = "monitor" ]]; then
  cd $TASC_HOME/cmd/monitor
  go build
  ./monitor
elif [[ "$ROLE" = "benchmark" ]]; then
  cd $TASC_HOME/benchmark
  go build
  python3 benchmark_server.py
elif [[ "$ROLE" = "lb" ]]; then
  cd $TASC_HOME/cmd/lb
  go build
  ./lb
fi
