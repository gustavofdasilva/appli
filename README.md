# Job Scout

Ferramenta pessoal que rastreia vagas de emprego em múltiplas fontes, usa um
LLM (via [OmniRoute](https://github.com/BunsDev/omniroute), um gateway de IA
self-hosted com endpoint compatível com OpenAI) para avaliar o fit de cada
vaga contra seu perfil e gera currículos personalizados para as vagas mais
promissoras — tudo rodando periodicamente em background, com um dashboard
web pra acompanhar.

Pipeline: **crawl → dedup → análise de fit (LLM) → notificação (Telegram) →
geração de currículo → dashboard**.

## Setup no Termux

```bash
pkg update && pkg upgrade
pkg install golang git pandoc python weasyprint
pip install weasyprint  # se necessário

git clone <url-do-repositorio>
cd job-scout
go mod tidy
```

`pandoc` + `weasyprint` são usados só na etapa de conversão do currículo de
Markdown pra PDF. Se algo der errado nessa instalação, o pipeline continua
funcionando normalmente — veja [Limitações conhecidas](#limitações-conhecidas).

## Configuração inicial

1. **Preencha o `config.yaml`**: endpoint e chave de API do OmniRoute
   (`omniroute_base_url`, `omniroute_api_key`), modelo a ser roteado
   (`omniroute_model`), expressão cron (`schedule`), `min_fit_score`,
   `server_port` e a lista de `crawlers` (quais fontes ficam habilitadas,
   termos de busca e número de páginas por termo). Veja
   `example.config.yaml` como referência.
   - `omniroute_api_key` é a chave gerada **dentro do OmniRoute** (dashboard
     do gateway), não a chave do provedor real — essa fica configurada
     dentro do próprio OmniRoute, que a usa pra rotear as chamadas pro
     modelo escolhido.
2. **(Opcional) Configure notificações no Telegram**: `telegram_bot_token`
   e `telegram_chat_id` — veja a seção [Notificações no
   Telegram](#notificações-no-telegram) abaixo. Deixe ambos vazios pra
   desabilitar.
3. **Preencha o `profile.md`** com suas informações reais — experiência,
   tecnologias, resultados quantificados. Esse arquivo é lido do disco a
   cada análise (nunca cacheado) e enviado como contexto pra LLM tanto na
   análise de fit quanto na geração de currículo, então evite dados
   sensíveis desnecessários (CPF, endereço completo etc).
4. **Rode**:
   ```bash
   go run .
   ```
5. **Acesse o dashboard**: [http://localhost:8080](http://localhost:8080)
   (ou a porta configurada em `server_port`).

Se `omniroute_api_key` estiver vazia, o job-scout continua rastreando e
salvando vagas normalmente — só pula as etapas de análise de fit e geração
de currículo, com um aviso no log.

## Rodar com Docker Compose

O `docker-compose.yml` sobe dois serviços: o `omniroute` (gateway de IA
self-hosted) e o `job-scout` (build a partir do `Dockerfile` deste repo).

1. Copie `example.config.yaml` para `config.yaml` e preencha os campos
   (o `omniroute_base_url` já vem apontado pra `http://omniroute:20128/v1`,
   o nome do serviço no compose — não precisa mudar).
2. Suba os serviços:
   ```bash
   docker compose up -d --build
   ```
3. Acesse o dashboard do OmniRoute em
   [http://localhost:20128](http://localhost:20128), configure o(s)
   provider(s) real(is) (ex: chave de API da Anthropic) e gere uma chave de
   API do OmniRoute — cole essa chave em `omniroute_api_key` no
   `config.yaml` e reinicie o job-scout (`docker compose restart job-scout`).
   - Se você configurar **mais de um provider** que ofereça o mesmo modelo
     (ex: dois providers com `claude-haiku-4-5`), o OmniRoute retorna `400
     Ambiguous model` pro nome "pelado". Nesse caso, veja em
     Providers/Models qual o slug de cada provider e prefixe
     `omniroute_model` com ele — ex: `kie/claude-haiku-4-5`.
4. Acesse o dashboard do job-scout em
   [http://localhost:8080](http://localhost:8080).

`config.yaml`, `profile.md` e o diretório `data/` (banco SQLite + currículos
gerados) são montados como volumes, então persistem fora do container e
podem ser editados sem rebuildar a imagem.

## Notificações no Telegram

O job-scout pode enviar uma mensagem no Telegram sempre que uma vaga
analisada atingir um `fit_score` mínimo — útil pra saber na hora das vagas
mais promissoras sem precisar ficar checando o dashboard.

1. **Crie um bot** conversando com [@BotFather](https://t.me/BotFather) no
   Telegram (`/newbot`) e copie o token gerado.
2. **Descubra seu `chat_id`**: envie qualquer mensagem pro bot recém-criado
   e acesse `https://api.telegram.org/bot<TOKEN>/getUpdates` — o campo
   `message.chat.id` da resposta é o seu `chat_id`.
3. **Preencha no `config.yaml`**:
   ```yaml
   telegram_bot_token: "<token do bot>"
   telegram_chat_id: "<seu chat_id>"
   telegram_min_score: 70 # opcional — default é o mesmo de min_fit_score
   ```
4. Reinicie o job-scout (ou `docker compose restart job-scout`, se estiver
   rodando via Docker).

Se `telegram_bot_token` ou `telegram_chat_id` estiverem vazios, as
notificações ficam desabilitadas e o pipeline continua normalmente. Falhas
no envio (bot inválido, rate limit do Telegram etc.) são apenas logadas como
aviso — nunca interrompem a análise ou a geração de currículo.

## Rodar em background no Termux

```bash
# Instalar tmux
pkg install tmux

# Nova sessão
tmux new -s jobscout
go run .
# Ctrl+B D pra destacar (a sessão continua rodando em background)
```

Pra voltar depois: `tmux attach -t jobscout`.

## Testar sem esperar o cron

Pelo dashboard: acesse `http://localhost:8080` e clique em **"Rodar
agora"**.

Ou via terminal:
```bash
curl -X POST http://localhost:8080/api/trigger
```

Isso dispara o pipeline completo imediatamente, fora do horário agendado.
Se um ciclo já estiver em execução (agendado ou manual), o disparo é
ignorado — não roda dois ciclos em paralelo.

## API

| Rota | Descrição |
|---|---|
| `GET /api/jobs?status=&min_score=` | Lista vagas + análise, ordenadas por fit_score |
| `GET /api/jobs/{id}` | Detalhes completos de uma vaga (job + analysis) |
| `GET /api/jobs/{id}/resume` | Serve o currículo em PDF (ou `.md` como fallback) |
| `POST /api/jobs/{id}/status` | Atualiza status (`new`/`reviewed`/`applied`/`ignored`) |
| `POST /api/trigger` | Dispara o pipeline imediatamente |
| `GET /api/status` | Último run, próximo run, contagem de vagas por status |

## Estrutura

```
internal/crawler/    fontes de vagas (Gupy, Indeed, RemoteOK, ProgramaThor, Trampos; LinkedIn é stub)
internal/analyzer/   análise de fit via LLM (gateway OmniRoute)
internal/resume/     geração de currículo (LLM -> Markdown -> PDF via pandoc)
internal/scheduler/  cron + orquestração do pipeline completo
internal/server/     API HTTP + dashboard estático (embutido no binário)
internal/storage/    persistência em SQLite
```

## Limitações conhecidas

- **LinkedIn não é suportado.** O crawler é um stub que retorna erro — o
  LinkedIn exige autenticação e tem proteções anti-scraping agressivas
  demais pra v1.
- **Indeed, ProgramaThor e Trampos dependem de scraping de HTML**, não de
  API pública. O markup desses sites muda com frequência e pode quebrar o
  parsing sem aviso — se um desses crawlers parar de encontrar vagas, os
  seletores CSS em `internal/crawler/*.go` são o primeiro lugar a revisar.
  Gupy e RemoteOK usam API pública/JSON e são mais estáveis.
- **PDF depende de `pandoc` + `weasyprint` instalados no sistema.** Se
  `pandoc` não for encontrado (ou falhar), o currículo em Markdown ainda é
  salvo em `data/resumes/{job_id}.md` e o pipeline continua normalmente —
  só não gera o PDF (`resume_pdf_path` fica apontando pro `.md`).
- **Dashboard e API não têm autenticação.** Qualquer pessoa com acesso à
  porta configurada pode ver vagas, mudar status e disparar o pipeline
  (que consome créditos do provedor de LLM configurado no OmniRoute). Rodar
  atrás de uma VPN/rede privada ou só em `localhost` é recomendado.
- **Sem suporte a múltiplos perfis ou múltiplos usuários** — o job-scout
  assume um único `profile.md` e um único banco SQLite local.
- **Sem paginação no `/api/jobs`** — a lista completa (filtrada) é
  retornada de uma vez. Pra volumes muito grandes de vagas acumuladas, a
  resposta e a renderização no dashboard podem ficar lentas.
- **Sem suíte de testes automatizados.** A validação até agora foi manual
  (builds, `go vet`, smoke tests pontuais contra APIs reais e contra um
  banco de dados de teste). Adicionar testes automatizados pros pacotes
  `crawler`, `analyzer`, `resume` e `scheduler` é um próximo passo natural.
- **Sem restart automático em caso de crash** no fluxo do Termux+tmux
  documentado acima — se o processo cair, é preciso reabrir a sessão tmux
  e rodar `go run .` de novo manualmente.
- **Shutdown gracioso não usa timeout pro pipeline em execução** — ao
  receber `SIGINT`/`SIGTERM`, o processo espera qualquer ciclo do pipeline
  em andamento (crawl + análise + geração de currículo) terminar antes de
  fechar o banco de dados. Isso evita corromper dados, mas significa que
  encerrar o processo durante um disparo manual pode demorar (o HTTP
  server, por outro lado, tem um timeout de shutdown de 10s).
