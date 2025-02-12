package server

var singleServerTemplate string = `
if [ -d "{{.ETCD_DIR}}" ]; then
	# if directory exists then it means its not an initial run
	/bin/k3s server --cluster-reset --config  {{.INIT_CONFIG}} {{.EXTRA_ARGS}}
fi
/bin/k3s server --config {{.INIT_CONFIG}} {{.EXTRA_ARGS}}`

var HAServerTemplate string = ` 
if [ ${POD_NAME: -1} == 0 ] && [ ! -d "{{.ETCD_DIR}}" ]; then
	/bin/k3s server --config {{.INIT_CONFIG}} {{.EXTRA_ARGS}}
else 
	/bin/k3s server --config {{.SERVER_CONFIG}} {{.EXTRA_ARGS}}
fi`
