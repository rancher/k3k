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

# Sentinel file checked by the liveness probe. run_k3s creates it when k3s
# reports it must rejoin the cluster, so the probe can restart the pod.
REJOIN_MARKER=/var/log/k3s-rejoin

# run_k3s starts the k3s server, streams its output to stdout ("kubectl logs" keeps working)
# and drops the sentinel file when the "rejoin the cluster" message appears.
# This replaces piping the full log to a growing file that the
# liveness probe used to grep on every check (see rancher/k3k#537).
run_k3s() {
	rm -f "$REJOIN_MARKER"

	/bin/k3s server "$@" 2>&1 | while IFS= read -r line; do
		printf '%s\n' "$line"
		case $line in
		*"rejoin the cluster"*)
			printf '1' > "$REJOIN_MARKER"
			;;
		esac
	done
}

# safe mode function to reset node IP after pod restarts
safe_mode() {
	CURRENT_IP=""
    if [ -f /var/lib/rancher/k3s/k3k-node-ip ]; then
        CURRENT_IP=$(cat /var/lib/rancher/k3s/k3k-node-ip)
    fi

    if [ -z "$CURRENT_IP" ] || [ "$CURRENT_IP" = "$POD_IP" ] || [ "{{.K3K_MODE}}" != "virtual" ]; then
        return
    fi

	# skipping if the node is starting for the first time
	if [ -d "{{.ETCD_DIR}}" ]; then

		info "Starting K3s in Safe Mode (Network Policy Disabled) to patch Node IP from ${CURRENT_IP} to ${POD_IP}"
		run_k3s --disable-network-policy --config $1 $EXTRA_ARGS > /dev/null 2>&1 &
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

		if ! run_k3s --cluster-reset --config {{.INIT_CONFIG}} $EXTRA_ARGS > /dev/null 2>&1; then
			fatal "cluster reset failed!"
		fi
		info "Cluster reset complete. Removing Reset flag file."
		rm -f /var/lib/rancher/k3s/server/db/reset-flag
	fi

	# entering safe mode to ensure correct NodeIP
	safe_mode {{.INIT_CONFIG}}

	info "Adding pod IP file."
	echo $POD_IP > /var/lib/rancher/k3s/k3k-node-ip

	run_k3s --config {{.INIT_CONFIG}} $EXTRA_ARGS
}

start_ha_node() {
	info "Starting pod $POD_NAME in HA node setup"

	if [ ${POD_NAME: -1} == 0 ] && [ ! -d "{{.ETCD_DIR}}" ]; then
		info "Adding pod IP file."
		echo $POD_IP > /var/lib/rancher/k3s/k3k-node-ip

		run_k3s --config {{.INIT_CONFIG}} $EXTRA_ARGS
	else
		safe_mode {{.SERVER_CONFIG}}

		info "Adding pod IP file."
		echo $POD_IP > /var/lib/rancher/k3s/k3k-node-ip

		run_k3s --config {{.SERVER_CONFIG}} $EXTRA_ARGS
	fi
}

# Configuring cgroups for k3s process in virtual mode
configure_cgroups() {
	runtime_class="{{.RUNTIME_CLASS}}"
	if [ "${runtime_class#kata}" != "$runtime_class" ]; then

		CGROUP_PATH=$(cat /proc/self/cgroup | cut -d: -f3)
		CGROUP_DIR="/sys/fs/cgroup${CGROUP_PATH}"

		# Move shell to init subcgroup to keep main cgroup clean for k3s children
		INIT_DIR="${CGROUP_DIR}init"
		mkdir -p "$INIT_DIR" 2>/dev/null

		PID=$(cut -d' ' -f4 /proc/self/stat)

		echo "$PID" > "$INIT_DIR/cgroup.procs"

		for controller in $(cat "$CGROUP_DIR/cgroup.controllers"); do
			echo "+$controller" > "$CGROUP_DIR/cgroup.subtree_control" 2>/dev/null || true
		done

		return
	fi

	# only configure the cgroups if the runtime used is the default and the mode is virtual
	# shared and hcp run agentless (no kubelet) and don't need cgroup overrides.
	if [ -n "$runtime_class" ] || [ "{{.K3K_MODE}}" != "virtual" ]; then	
		return
	fi

	root_cgroup_raw=$(cat /proc/self/cgroup)
	root_cgroup_stripped="${root_cgroup_raw#0::}"
	root_cgroup_parent=$(dirname "$root_cgroup_stripped")

	info "Current CGROUPS for $POD_NAME: ${root_cgroup_raw}"

	# overriding kubelet cgroup and the cgroup root for pods, this will prevent k3s
	# automatic placement see: https://github.com/k3s-io/k3s/blob/main/pkg/cgroups/cgroups_linux.go#L114-L127
	EXTRA_ARGS="$EXTRA_ARGS --kubelet-arg=kubelet-cgroups=$root_cgroup_parent/k3s --kubelet-arg=cgroup-root=$root_cgroup_parent"
}

EXTRA_ARGS="{{.EXTRA_ARGS}}"
configure_cgroups

if [ ! -f /etc/machine-id ]; then
	MACHINE_ID=$(printf '%s' "$POD_NAME" | md5sum | cut -d' ' -f1)
	printf '%s' "$MACHINE_ID" > /etc/machine-id
fi

case "{{.CLUSTER_MODE}}" in
    "ha")
        start_ha_node
        ;;
    "single"|*)
        start_single_node
        ;;
esac`
