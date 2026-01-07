package server

var StartupCommand string = `
info()
{
    echo "[INFO] [$(date +"%c")]" "$@"
}

fatal()
{
    echo "[FATAL] [$(date +"%c")] " "$@" >&2
    exit 1
}

# safe mode function to reset node IP after pod restarts
safe_mode() {
	CURRENT_IP=""
    if [ -f /var/lib/rancher/k3s/k3k-node-ip ]; then
        CURRENT_IP=$(cat /var/lib/rancher/k3s/k3k-node-ip)
    fi

    if [ -z "$CURRENT_IP" ] || [ "$CURRENT_IP" = "$POD_IP" ] || [ {{.K3K_MODE}} != "virtual" ]; then
        return
    fi

	# skipping if the node is starting for the first time
	if [ -d "{{.ETCD_DIR}}" ]; then

		info "Starting K3s in Safe Mode (Network Policy Disabled) to patch Node IP from ${CURRENT_IP} to ${POD_IP}"
		/bin/k3s server --disable-network-policy --config $1 {{.EXTRA_ARGS}} > /dev/null 2>&1 &
		PID=$!

		# Start the loop to wait for the nodeIP to change
		info "Waiting for Node IP to update to ${POD_IP}."
		count=0
		until kubectl get nodes -o wide 2>/dev/null | grep -q "${POD_IP}"; do
			if ! kill -0 $PID 2>/dev/null; then
				fatal "safe Mode K3s process died unexpectedly!"
			fi
			sleep 2
			count=$((count+1))

			if [ $count -gt 60 ]; then
				fatal "timed out waiting for node to change IP from $CURRENT_IP to $POD_IP"
			fi
		done
		
		info "Node IP is set to ${POD_IP} successfully. Stopping Safe Mode process..."
		kill $PID
		wait $PID 2>/dev/null || true
	fi
}

start_single_node() {
	info "Starting single node setup..."

	# checking for existing data in single server if found we must perform reset
	if [ -d "{{.ETCD_DIR}}" ]; then
		info "Existing data found in single node setup. Performing cluster-reset to ensure quorum..."

		if ! /bin/k3s server --cluster-reset --config {{.INIT_CONFIG}} {{.EXTRA_ARGS}} > /dev/null 2>&1; then
			fatal "cluster reset failed!"
		fi
		info "Cluster reset complete. Removing Reset flag file."
		rm -f /var/lib/rancher/k3s/server/db/reset-flag
	fi

	# entering safe mode to ensure correct NodeIP
	safe_mode {{.INIT_CONFIG}}

	info "Adding pod IP file."
	echo $POD_IP > /var/lib/rancher/k3s/k3k-node-ip

	/bin/k3s server --config {{.INIT_CONFIG}} {{.EXTRA_ARGS}} 2>&1 | tee /var/log/k3s.log
}

start_ha_node() {
	info "Starting pod $POD_NAME in HA node setup"

	if [ ${POD_NAME: -1} == 0 ] && [ ! -d "{{.ETCD_DIR}}" ]; then
		info "Adding pod IP file."
		echo $POD_IP > /var/lib/rancher/k3s/k3k-node-ip

		/bin/k3s server --config {{.INIT_CONFIG}} {{.EXTRA_ARGS}} 2>&1 | tee /var/log/k3s.log
	else
		safe_mode {{.SERVER_CONFIG}}

		info "Adding pod IP file."
		echo $POD_IP > /var/lib/rancher/k3s/k3k-node-ip

		/bin/k3s server --config {{.SERVER_CONFIG}} {{.EXTRA_ARGS}} 2>&1 | tee /var/log/k3s.info 
	fi
}

case "{{.CLUSTER_MODE}}" in
    "ha")
        start_ha_node
        ;;
    "single"|*)
        start_single_node
        ;;
esac`
