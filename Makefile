.PHONY: help \
	gate-ci-fast gate-ci-full \
	gate-local-quick gate-staging \
	gate-staging-full gate-staging-full-strict \
	gate-contract-matrix gate-local-quick-with-contract-matrix \
	gate-bundle-local \
	gate-ubuntu22-baseline gate-linux-baseline \
	rollout-mesh-msk rollout-mesh-edg rollout-mesh-vrn \
	gate-mesh-msk gate-mesh-edg gate-mesh-vrn gate-mesh-prod gate-mesh-prod-release \
	provision-support-k2 release-validate-prod-mesh

GATE := ./scripts/release_gate_runtime_helper.sh
BUNDLE_GATE := ./scripts/bootstrap_runtime_helper_gate_bundle.sh
UBUNTU22_BASELINE_GATE := ./scripts/ops_sre_ubuntu22_baseline_gate.sh
LINUX_BASELINE_GATE := ./scripts/ops_sre_linux_baseline_gate.sh
ROLLOUT_MESH_MSK := ./scripts/rollout_tunrnd_msk_mesh.sh
ROLLOUT_MESH_EDG := ./scripts/rollout_tunrnd_edg_mesh.sh
ROLLOUT_MESH_VRN := ./scripts/rollout_tunrnd_vrn_mesh.sh
GATE_MESH_MSK := ./scripts/gate_tunrnd_mesh.sh
GATE_MESH_EDG := ./scripts/gate_tunrnd_edg_mesh.sh
GATE_MESH_VRN := ./scripts/gate_tunrnd_vrn_mesh.sh
GATE_MESH_PROD := ./scripts/gate_tunrnd_prod_mesh.sh
GATE_MESH_PROD_RELEASE := ./scripts/gate_tunrnd_prod_release.sh
PROVISION_SUPPORT_K2 := ./scripts/provision_support_signing_k2.sh
RELEASE_VALIDATE_PROD_MESH := ./scripts/release_validation_tunrnd_prod_mesh.sh
CONTRACT_MATRIX := ./scripts/runtime_helper_contract_check_matrix.sh
OUT_DIR ?= ./artifacts/runtime-helper-gate
SUPPORT_KEY_FILE ?=
CONTRACT_SCHEMA_CURRENT ?= 2026-04-19
CONTRACT_SCHEMA_NEXT ?= 2026-04-20
CONTRACT_NEXT_ALLOW_FAIL ?= true

help:
	@echo "Available targets:"
	@echo "  make gate-ci-fast             # go tests only (ci-fast profile)"
	@echo "  make gate-ci-full             # go tests + local helper smokes (ci-full profile)"
	@echo "  make gate-local-quick         # local quick gate (skip support/canary)"
	@echo "  make gate-staging             # staging profile (skip support by default)"
	@echo "  make gate-staging-full BUNDLE=<path> [ACTIVE_KEY=<id=path>]"
	@echo "  make gate-staging-full-strict BUNDLE=<path> [ACTIVE_KEY=<id=path>] [REPORT=<path>]"
	@echo "  make gate-contract-matrix [CONTRACT_SCHEMA_CURRENT=2026-04-19] [CONTRACT_SCHEMA_NEXT=2026-04-20] [CONTRACT_NEXT_ALLOW_FAIL=true]"
	@echo "  make gate-local-quick-with-contract-matrix"
	@echo "  make gate-bundle-local [OUT_DIR=./artifacts/runtime-helper-gate]"
	@echo "  make gate-ubuntu22-baseline [SUPPORT_KEY_FILE=/etc/tun/support-signing-k2.key]"
	@echo "  make gate-linux-baseline [SUPPORT_KEY_FILE=/etc/tun/support-signing-k2.key]"
	@echo "  make rollout-mesh-msk"
	@echo "  make rollout-mesh-edg"
	@echo "  make rollout-mesh-vrn"
	@echo "  make gate-mesh-msk"
	@echo "  make gate-mesh-edg"
	@echo "  make gate-mesh-vrn"
	@echo "  make gate-mesh-prod"
	@echo "  make gate-mesh-prod-release"
	@echo "  make provision-support-k2 [KEY_FILE=/path/to/key]"
	@echo "  make release-validate-prod-mesh [PROFILE=quick]"

gate-ci-fast:
	$(GATE) --profile ci-fast

gate-ci-full:
	HELPER_SMOKE_TRANSPORT=unix $(GATE) --profile ci-full

gate-local-quick:
	$(GATE) --profile local --skip-support-bundle-gate --skip-autopilot-canary

gate-contract-matrix:
	$(CONTRACT_MATRIX) \
	  --current-schema-version "$(CONTRACT_SCHEMA_CURRENT)" \
	  --next-schema-version "$(CONTRACT_SCHEMA_NEXT)" \
	  --allow-next-fail "$(CONTRACT_NEXT_ALLOW_FAIL)"

gate-local-quick-with-contract-matrix: gate-contract-matrix gate-local-quick

gate-staging:
	$(GATE) --profile staging --skip-support-bundle-gate

gate-staging-full:
	@if [ -z "$(BUNDLE)" ]; then echo "BUNDLE is required"; exit 2; fi
	@if [ -n "$(ACTIVE_KEY)" ]; then \
	  $(GATE) --profile staging-full --bundle "$(BUNDLE)" --active-key "$(ACTIVE_KEY)"; \
	else \
	  $(GATE) --profile staging-full --bundle "$(BUNDLE)"; \
	fi

gate-staging-full-strict:
	@if [ -z "$(BUNDLE)" ]; then echo "BUNDLE is required"; exit 2; fi
	@if [ -n "$(ACTIVE_KEY)" ] && [ -n "$(REPORT)" ]; then \
	  $(GATE) --profile staging-full-strict --bundle "$(BUNDLE)" --active-key "$(ACTIVE_KEY)" --report-file "$(REPORT)"; \
	elif [ -n "$(ACTIVE_KEY)" ]; then \
	  $(GATE) --profile staging-full-strict --bundle "$(BUNDLE)" --active-key "$(ACTIVE_KEY)"; \
	elif [ -n "$(REPORT)" ]; then \
	  $(GATE) --profile staging-full-strict --bundle "$(BUNDLE)" --report-file "$(REPORT)"; \
	else \
	  $(GATE) --profile staging-full-strict --bundle "$(BUNDLE)"; \
	fi

gate-bundle-local:
	$(BUNDLE_GATE) --profile local --out-dir "$(OUT_DIR)" --gate-arg "--skip-support-bundle-gate" --gate-arg "--skip-autopilot-canary"

gate-ubuntu22-baseline:
	@if [ -n "$(SUPPORT_KEY_FILE)" ]; then \
	  $(UBUNTU22_BASELINE_GATE) --support-key-file "$(SUPPORT_KEY_FILE)"; \
	else \
	  $(UBUNTU22_BASELINE_GATE); \
	fi

gate-linux-baseline:
	@if [ -n "$(SUPPORT_KEY_FILE)" ]; then \
	  $(LINUX_BASELINE_GATE) --support-key-file "$(SUPPORT_KEY_FILE)"; \
	else \
	  $(LINUX_BASELINE_GATE); \
	fi

rollout-mesh-msk:
	$(ROLLOUT_MESH_MSK)

rollout-mesh-edg:
	$(ROLLOUT_MESH_EDG)

rollout-mesh-vrn:
	$(ROLLOUT_MESH_VRN)

gate-mesh-msk:
	$(GATE_MESH_MSK)

gate-mesh-edg:
	$(GATE_MESH_EDG)

gate-mesh-vrn:
	$(GATE_MESH_VRN)

gate-mesh-prod:
	$(GATE_MESH_PROD)

gate-mesh-prod-release:
	$(GATE_MESH_PROD_RELEASE)

provision-support-k2:
	@if [ -n "$(KEY_FILE)" ]; then \
	  $(PROVISION_SUPPORT_K2) --key-file "$(KEY_FILE)"; \
	else \
	  $(PROVISION_SUPPORT_K2); \
	fi

release-validate-prod-mesh:
	@if [ -n "$(PROFILE)" ]; then \
	  $(RELEASE_VALIDATE_PROD_MESH) --profile "$(PROFILE)"; \
	else \
	  $(RELEASE_VALIDATE_PROD_MESH); \
	fi
