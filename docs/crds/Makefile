CRD_REF_DOCS_VER := v0.1.0
CRD_REF_DOCS := go run github.com/elastic/crd-ref-docs@$(CRD_REF_DOCS_VER)

.PHONY: generate
generate:
	$(CRD_REF_DOCS) --config=config.yaml --renderer=markdown --source-path=../../pkg/apis/k3k.io/v1alpha1 --output-path=crd-docs.md
