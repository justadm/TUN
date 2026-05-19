.PHONY: help up down logs ps api-shell ingestor-shell smoke-command

help:
	@echo "Monitoring dev targets:"
	@echo "  make up            # start local monitoring stack"
	@echo "  make down          # stop local monitoring stack"
	@echo "  make logs          # tail compose logs"
	@echo "  make ps            # show compose status"
	@echo "  make api-shell     # shell into monitoring-api container"
	@echo "  make ingestor-shell # shell into monitoring-ingestor container"
	@echo "  make smoke-command LINK_ID=<id> [COMMAND=reconnect] # e2e command smoke"

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f

ps:
	docker compose ps

api-shell:
	docker compose exec monitoring-api sh

ingestor-shell:
	docker compose exec monitoring-ingestor sh

smoke-command:
	@if [ -z "$(LINK_ID)" ]; then echo "LINK_ID is required"; exit 2; fi
	MONITORING_API_BASE=http://127.0.0.1:$${MONITORING_API_PORT:-18070} \
	./scripts/commands_e2e_smoke.sh "$(LINK_ID)" "$(if $(COMMAND),$(COMMAND),reconnect)"
