package server

var singleServerTemplate string = `
if [ -d "{{.ETCD_DIR}}" ]; then
	# if directory exists then it means its not an initial run
	/bin/k3s server --cluster-reset --config  {{.INIT_CONFIG}} {{.EXTRA_ARGS}} 2>&1 | tee /var/log/k3s.log
fi
rm -f /var/lib/rancher/k3s/server/db/reset-flag
/bin/k3s server --config {{.INIT_CONFIG}} {{.EXTRA_ARGS}} 2>&1 | tee /var/log/k3s.log`

var HAServerTemplate string = ` 
if [ ${POD_NAME: -1} == 0 ] && [ ! -d "{{.ETCD_DIR}}" ]; then
	/bin/k3s server --config {{.INIT_CONFIG}} {{.EXTRA_ARGS}} 2>&1 | tee /var/log/k3s.log
else 
	/bin/k3s server --config {{.SERVER_CONFIG}} {{.EXTRA_ARGS}} 2>&1 | tee /var/log/k3s.log 
fi`
