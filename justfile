set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

gate_script := "./scripts/release_gate_runtime_helper.sh"
bundle_gate_script := "./scripts/bootstrap_runtime_helper_gate_bundle.sh"
ubuntu22_baseline_gate_script := "./scripts/ops_sre_ubuntu22_baseline_gate.sh"
linux_baseline_gate_script := "./scripts/ops_sre_linux_baseline_gate.sh"
rollout_mesh_msk_script := "./scripts/rollout_tunrnd_msk_mesh.sh"
rollout_mesh_edg_script := "./scripts/rollout_tunrnd_edg_mesh.sh"
rollout_mesh_vrn_script := "./scripts/rollout_tunrnd_vrn_mesh.sh"
gate_mesh_msk_script := "./scripts/gate_tunrnd_mesh.sh"
gate_mesh_edg_script := "./scripts/gate_tunrnd_edg_mesh.sh"
gate_mesh_vrn_script := "./scripts/gate_tunrnd_vrn_mesh.sh"
gate_mesh_prod_script := "./scripts/gate_tunrnd_prod_mesh.sh"
gate_mesh_prod_release_script := "./scripts/gate_tunrnd_prod_release.sh"
provision_support_k2_script := "./scripts/provision_support_signing_k2.sh"
release_validate_prod_mesh_script := "./scripts/release_validation_tunrnd_prod_mesh.sh"
contract_matrix_script := "./scripts/runtime_helper_contract_check_matrix.sh"

default:
  @echo "Available recipes:"
  @echo "  just gate-ci-fast"
  @echo "  just gate-ci-full"
  @echo "  just gate-local-quick"
  @echo "  just gate-staging"
  @echo "  just gate-staging-full <bundle> [active_key]"
  @echo "  just gate-staging-full-strict <bundle> [active_key] [report_file]"
  @echo "  just gate-contract-matrix [current_schema] [next_schema] [allow_next_fail]"
  @echo "  just gate-local-quick-with-contract-matrix [current_schema] [next_schema] [allow_next_fail]"
  @echo "  just gate-bundle-local [out_dir]"
  @echo "  just gate-ubuntu22-baseline [support_key_file]"
  @echo "  just gate-linux-baseline [support_key_file]"
  @echo "  just rollout-mesh-msk"
  @echo "  just rollout-mesh-edg"
  @echo "  just rollout-mesh-vrn"
  @echo "  just gate-mesh-msk"
  @echo "  just gate-mesh-edg"
  @echo "  just gate-mesh-vrn"
  @echo "  just gate-mesh-prod"
  @echo "  just gate-mesh-prod-release"
  @echo "  just provision-support-k2 [key_file]"
  @echo "  just release-validate-prod-mesh [profile]"

gate-ci-fast:
  {{gate_script}} --profile ci-fast

gate-ci-full:
  HELPER_SMOKE_TRANSPORT=unix {{gate_script}} --profile ci-full

gate-local-quick:
  {{gate_script}} --profile local --skip-support-bundle-gate --skip-autopilot-canary

gate-contract-matrix current_schema='2026-04-19' next_schema='2026-04-20' allow_next_fail='true':
  {{contract_matrix_script}} \
    --current-schema-version "{{current_schema}}" \
    --next-schema-version "{{next_schema}}" \
    --allow-next-fail "{{allow_next_fail}}"

gate-local-quick-with-contract-matrix current_schema='2026-04-19' next_schema='2026-04-20' allow_next_fail='true':
  just gate-contract-matrix "{{current_schema}}" "{{next_schema}}" "{{allow_next_fail}}"
  just gate-local-quick

gate-staging:
  {{gate_script}} --profile staging --skip-support-bundle-gate

gate-staging-full bundle active_key='':
  if [[ -n "{{active_key}}" ]]; then
    {{gate_script}} --profile staging-full --bundle "{{bundle}}" --active-key "{{active_key}}"
  else
    {{gate_script}} --profile staging-full --bundle "{{bundle}}"
  fi

gate-staging-full-strict bundle active_key='' report_file='':
  if [[ -n "{{active_key}}" && -n "{{report_file}}" ]]; then
    {{gate_script}} --profile staging-full-strict --bundle "{{bundle}}" --active-key "{{active_key}}" --report-file "{{report_file}}"
  elif [[ -n "{{active_key}}" ]]; then
    {{gate_script}} --profile staging-full-strict --bundle "{{bundle}}" --active-key "{{active_key}}"
  elif [[ -n "{{report_file}}" ]]; then
    {{gate_script}} --profile staging-full-strict --bundle "{{bundle}}" --report-file "{{report_file}}"
  else
    {{gate_script}} --profile staging-full-strict --bundle "{{bundle}}"
  fi

gate-bundle-local out_dir='./artifacts/runtime-helper-gate':
  {{bundle_gate_script}} --profile local --out-dir "{{out_dir}}" --gate-arg "--skip-support-bundle-gate" --gate-arg "--skip-autopilot-canary"

gate-ubuntu22-baseline support_key_file='':
  if [[ -n "{{support_key_file}}" ]]; then
    {{ubuntu22_baseline_gate_script}} --support-key-file "{{support_key_file}}"
  else
    {{ubuntu22_baseline_gate_script}}
  fi

gate-linux-baseline support_key_file='':
  if [[ -n "{{support_key_file}}" ]]; then
    {{linux_baseline_gate_script}} --support-key-file "{{support_key_file}}"
  else
    {{linux_baseline_gate_script}}
  fi

rollout-mesh-msk:
  {{rollout_mesh_msk_script}}

rollout-mesh-edg:
  {{rollout_mesh_edg_script}}

rollout-mesh-vrn:
  {{rollout_mesh_vrn_script}}

gate-mesh-msk:
  {{gate_mesh_msk_script}}

gate-mesh-edg:
  {{gate_mesh_edg_script}}

gate-mesh-vrn:
  {{gate_mesh_vrn_script}}

gate-mesh-prod:
  {{gate_mesh_prod_script}}

gate-mesh-prod-release:
  {{gate_mesh_prod_release_script}}

provision-support-k2 key_file='':
  if [[ -n "{{key_file}}" ]]; then
    {{provision_support_k2_script}} --key-file "{{key_file}}"
  else
    {{provision_support_k2_script}}
  fi

release-validate-prod-mesh profile='':
  if [[ -n "{{profile}}" ]]; then
    {{release_validate_prod_mesh_script}} --profile "{{profile}}"
  else
    {{release_validate_prod_mesh_script}}
  fi
