# Интеграция AI-терминала с DeepSeek Harness: проверенный API

> Подготовка Phase C (задачи 9–10 плана). Источник — **установленный** DeepSeek Harness Desktop
> **0.2.2-rc.1** (/Applications/DeepSeek Harness.app): app.asar (213 МБ), пакеты
> /dsh/node_modules/@deepseek-ai/*. Код терминала и плагина в этой задаче **не менялся** —
> только документ.
>
> Статусы фактов: **S** — подтверждено чтением кода/README установленного Harness (рядом
> координата); **I** — вывод/интерпретация, для приёмки нужен живой плагин или доп. проверка.
>
> Координаты даны внутри app.asar: `…/<package>/README.md:<строка>` и
> `…/<package>/lib/index.js:<строка>` (или lib/client.js), где `…` =
> `/dsh/node_modules/@deepseek-ai/`. Файлов `*.d.ts` в сборке нет; страницы docs/subsystems/*,
> на которые ссылаются README, в архив не входят — факты ниже проверялись по README пакетов и по
> фактическому bundled JS.

## 1. Метод проверки

- app.asar разобран по заголовку (asar/pickle): JSON-оглавление + данные файлов; извлечены все
  README.md и package.json 332 пакетов @deepseek-ai/* и код 17 ключевых пакетов (310 файлов,
  ~5.5 МБ) во временный каталог для точечного grep.
- Проверено: наличие сервисов (ctx.*), формы вызовов из README/JSDoc bundled JS, регистрация
  слотов и компонентов в Client-бандлах, манифест плагина и слои композиции (dsh.bundle.patch).
- Не проверено: живой запуск плагина в Harness, Remote-вызовы, точные TypeScript-типы
  (в asar нет *.d.ts), страницы docs/subsystems/*.

## 2. Host API (сервисы ctx.*)

### 2.1 ctx.tools и defineTool — S

**Координаты:** dsh-tools/README.md:29-99; реализация — dsh-tools/lib/index.js:311-360
(defineTool), :229-284 (компиляция схем), :59-228 (DSL-компилятор).

Регистрация инструмента (dsh-tools/README.md:39-63):

~~~js
import { defineTool } from '@deepseek-ai/dsh-tools'

ctx.tools.register(defineTool({
  name: 'terminal_get_quote',
  description: 'Котировка и стакан инструмента терминала.',
  parameters: {
    symbol: { type: 'string', required: true, description: 'BOARD:SECCODE' },
    limit:  { type: 'number' },
  },
  output: {
    schema: { type: 'string' },
    render: (_args, value) => [{ type: 'text', text: value }],
  },
  async execute(args, exec) {
    // exec.signal — caller-owned AbortSignal; его нужно соблюдать
    return await callBackend('/api/agent/tools/terminal_get_quote/invoke', args, exec.signal)
  },
}))
~~~

Подтверждённые факты:

- ctx.tools.register(definition, { when? }) возвращает disposer; when — синхронная/асинхронная
  проверка **на каждый model step** (S: dsh-tools/README.md:31,39-63,70).
- defineTool принимает **собственный DSL схем**, а не готовую JSON Schema: на входе parameters —
  объект «поле → value-schema», output.schema — value-schema; наружу компилируется в JSON Schema
  (S: dsh-tools/lib/index.js:311-321; DSL — :59-228). Для type: 'object' DSL **требует явный**
  additionalProperties: true|false, а обязательность поля задаётся булевым required: true
  **внутри схемы поля**, не JSON-Schema-массивом (S: dsh-tools/lib/index.js:75-77,165-180).
- output.render(args, value) строит model-facing контент (массив блоков {type:'text',text});
  output.presentationMeta — опциональные метаданные карточки
  (S: dsh-tools/lib/index.js:311-333).
- execute(args, exec): exec.signal — кооперативная отмена; exec.agent — Agent, от имени которого
  вызван инструмент (S: dsh-tools/README.md:58-61; dsh-tool-schedule/README.md:73
  «Execution reads exec.agent for the acting Session»).
- presentCall()/presentResult() остаются для Host-потребителей, **Web Client их не использует**:
  карточку рисует слот tool.call.toolview по аргументам/контенту/мете (S: dsh-tools/README.md:99).
- Model-visible result — канонический JSON объявленного output.schema; ошибка инструмента
  становится обычным error result и не завершает turn (S: dsh-tools/README.md:35-37,129).

### 2.2 ctx.llm.stream — S

**Координаты:** dsh-llm/README.md:48-96; виды чанков — dsh-llm/lib/index.js
(block-start, text-delta, block-end, tool-call, reasoning-delta, usage, finish).

~~~js
for await (const chunk of ctx.llm.stream({
  provider: 'deepseek-official',
  model: 'deepseek-flash',
  messages: [{ role: 'user', content: [{ type: 'text', text: 'Привет' }] }],
  temperature: 0.2,
  maxTokens: 2000,
})) {
  // chunk.kind: block-start | text-delta | block-end | tool-call | reasoning-delta | usage | finish
}
~~~

- Аргументы: provider, model, messages (durable Message с content-блоками), опционально tools
  (активные определения), temperature, maxTokens, stop, purpose, sessionId
  (S: dsh-llm/README.md:51-58; набор сэмплинга — :180).
- Стрим всегда заканчивается одним терминальным finish {kind:'error'|'aborted'|...}; коды ошибок
  стабильные (NO_ADAPTER, MISSING_CREDENTIAL, AUTH, RATE_LIMIT, CONTEXT_WINDOW_EXCEEDED, QUOTA…);
  сервис **не** повторяет запрос (S: dsh-llm/README.md:94-96).
- Tool-call приходит чанками {type:'tool-call', id, name, argumentsDelta, index}; сборка — через
  BlockAssembler, результирующий блок type:'tool-call' с toolCallId/toolCallName/toolCallArguments
  (S: dsh-llm/lib/index.js:985-1000,1032-1036).
- tool_choice отсутствует; tools — «active definitions», исторические объявления требуют
  toolHistory из Session (S: dsh-llm/README.md:180-184). Для моста LLM вне agent-loop это значит:
  схемы передать можно, но tool-calls модели нужно собирать самому.
- ctx.llm.listProviders() — зарегистрированные маршруты (S: dsh-llm/README.md:60);
  prepareCall(config, signal, configure?) — подготовка вызова (S: dsh-llm/README.md:66-83).
- В Web-композиции по умолчанию выбраны deepseek-official / deepseek-flash
  (S: dsh-web-app/README.md:44).

### 2.3 ctx.skills — S

**Координаты:** dsh-skill/README.md:29-69; реализация — dsh-skill/lib/index.js:120-190.

- ctx.skills.registerProvider(create) — **синхронная** регистрация во время apply(); create(control)
  — фабрика, получает control = { signal, invalidate() } и возвращает провайдера { name, list(),
  get() }; возвращает disposer (S: dsh-skill/lib/index.js:142-181; README проще: :54).
- ctx.skills.register(skill) — встроенный (runtime) навык в слое вызывающего контекста
  (S: dsh-skill/README.md:53).
- Каталоги dsh-skill-filesystem: <project>/.dsh/skills, <project>/.agents/skills,
  Config.customSkillDirs, <dshHome>/skills, <agentsHome>/skills (ранги 100–500); формат —
  <name>/SKILL.md или <name>.md с YAML-frontmatter name/description
  (S: dsh-skill-filesystem/README.md:35-58).
- Реестр мержит провайдеров слоями; get() при загрузке перечитывает тело, тела не кешируются;
  инвалидация — control.invalidate() (S: dsh-skill/README.md:49-69,95-104).

### 2.4 ctx.workflowEngine — S

**Координаты:** dsh-workflow/README.md:31-58.

- ctx.workflowEngine.start({ script, meta, args?, parent, signal? }) — запуск JS-скрипта
  (не TypeScript, top-level await, return <json>); meta/args — JSON-данные; parent атрибутирует
  детей вызывающему агенту, signal отменяет run (S: dsh-workflow/README.md:52).
- Возвращённый run: { id, meta, result, cancel(reason?), dispose() }; result **никогда не reject**
  (ошибка — stopReason:'error', отмена — 'cancelled'); каждый run обязан быть dispose()-нут
  вызывающим (S: dsh-workflow/README.md:54).
- Хуки скрипта: agent(), parallel(), pipeline(), phase(), log()
  (S: dsh-workflow/README.md:33-46).

### 2.5 ctx.schedule — S

**Координаты:** dsh-schedule/README.md:50-83; инструменты — dsh-tool-schedule/README.md.

- Создание: create({ title, prompt, timing, target? }, actorSessionId?, signal?); timing.kind —
  after|at|every|daily|weekly|cron|rrule, принимается только соответствующий селектор
  (S: dsh-schedule/README.md:64-70). title ≤ 120 символов, prompt непустой (:70).
- Управление: edit, list, catalog, view, history, run (S: dsh-schedule/README.md:74-78).
- Сервис требует agents, sessions, storageDomain, sessionPersistence; в Web-композиции
  sessionController инжектится как fallback/creation provider для новых destinations
  (S: dsh-schedule/README.md:54). Хранилище — <DSH_HOME>/storages/schedule.json (:81-83).

### 2.6 ctx.sessionController — S (только Web-композиция)

**Координаты:** dsh-api-session-controller/lib/index.js:3791 (регистрация сервиса
super(ctx, "sessionController", { namespace: "session" })), :235 (async createSession(request)),
:831 (async prompt(request, source)).

- createSession(request) (request: sessionId?, cwd, agentPreset; возвращает Session + preset;
  :235-260) и prompt(request, source) (request: sessionId, content-блоки, delivery mode,
  requestId; source — durable user-rpc; :825-840) — те же методы, что вызывают Remote-endpoint'ы
  Web-клиента через typert (S: dsh-api-session-controller/lib/typert.remote-client.js:1893+).
- Сервис живёт в композиции пакета dsh-api-session-controller (Web); Schedule использует его как
  creation provider (S: dsh-schedule/lib/index.js:1501,2118-2121,2518).
- Вне Web-композиции альтернатива: ctx.sessions.create() + ctx.agents.create()
  (S: dsh-session/README.md:33-41, dsh-agent/README.md:33-44). Точный DTO createSession в README
  не опубликован — детали I.

### 2.7 ctx.agents — S

**Координаты:** dsh-agent/README.md:29-79.

- ctx.agents.create(), resume(), get(id), list(), roots(), isOwnedBy(); create/resume возвращают
  AgentHandle — единственный объект, который может погасить агента (handle.dispose())
  (S: dsh-agent/README.md:33-44).
- Разговор: handle.agent.followup({content, source}), steer(), inject(), cancel(cause),
  whenIdle() (S: dsh-agent/README.md:57-71).
- Agent.ctx — scoped-контекст: регистрации инструментов/prompt-секций/слушателей действуют только
  на этого агента (S: dsh-agent/README.md:73-75).
- agent.model.select({provider, model, reasoningEffort?}) — выбор модели на следующий step
  (S: dsh-agent/README.md:46-53).

### 2.8 ctx.storageDomain — S

**Координаты:** dsh-storage-domain/README.md:27-74.

- Домен объявляется один раз defineDomain({name, version, tables:{...}}), потребитель открывает:
  const domain = await ctx.storageDomain.open(spec); далее domain.table(t).put/get/update,
  domain.close(); чтения синхронные, записи durable и эмитят domain/changed
  (S: dsh-storage-domain/README.md:35-59).
- Маршрутизация бэкендов — конфигом плагина (backend, routes); ошибки — DomainError (already-open,
  invalid-record, missing-key, closed…) (S: dsh-storage-domain/README.md:61-74).

### 2.9 ctx.subprocess — S (важно для плагина dsh-ai-terminal)

**Координаты:** dsh-subprocess/README.md:38-96; env — :79-85; запуск — :42-57.

- const handle = ctx.subprocess.spawn({ argv, cwd, stdio, graceMs, env?, signal? }) — handle сразу;
  await handle.done даёт {exitCode, signal}; handle.collected.stdout.readFrom(0),
  handle.terminate(), handle.waitForExit() (S: dsh-subprocess/README.md:42-57,61-70).
- **Env-политика:** «Children never inherit the harness's ambient secrets: credential-shaped names
  and ambient DSH_* facts are scrubbed, and the caller's explicit env merges after that scrub»
  (S: dsh-subprocess/README.md:79-85). Scrub-паттерн — /KEY|PASSWORD|SECRET|TOKEN/i
  (S: dsh-subprocess/lib/index.js — SENSITIVE_ENV_PATTERN); merge —
  childEnv(extra) = { ...scrubbedParentEnv(), ...extra }
  (S: dsh-subprocess-local/lib/runner-launch-*.js, function childEnv). Вывод для плагина:
  TERMINAL_API_TOKEN, TERMINAL_INSTANCE_ID и терминальные TRANSAQ_*/TC_*/LLM-креды передавать
  **явно** в env, иначе ambient-значения вычищаются (S; в dsh-ai-terminal/index.js уже так).
- ctx.subprocess.resolveExecutable('go') — резолв исполняемого файла в execution world
  (S: dsh-subprocess/README.md:46-47).

### 2.10 ctx.webServer — S

**Координаты:** dsh-host-webserver/README.md:27-71.

- ctx.webServer.register(route) добавляет именованный HTTP-маршрут exact или prefix,
  registerUpgrade(route) — upgrade-маршрут; оба возвращают disposer; дубликат пути в одной таблице
  бросает (S: dsh-host-webserver/README.md:60-62).
- ctx.webServer.port, ctx.webServer.protocol (http:/https:), ctx.webServer.getOrigin() — фактический
  адрес слушателя (S: dsh-host-webserver/README.md:40,58).
- Маршрут может отдавать handler(req,res) или resource(request, peer?) → Fetch Response (GET/HEAD)
  (S: dsh-host-webserver/README.md:63-64).

### 2.11 ctx.sessions — S

**Координаты:** dsh-session/README.md:27-89.

- ctx.sessions.create(id, {meta:{cwd}}), get, list, fork, flush; session.append(type, data, opts?),
  session.deriveMessages() — модель-видимая история (S: dsh-session/README.md:31-51,81-89).
  Плагину для чата обычно достаточно ctx.agents; прямой доступ к Session нужен для собственных
  event-типов/проекций.

## 3. Client API (браузерная половина плагина)

### 3.1 Манифест клиентского плагина и bundle patch — S

**Координаты:** dsh-package-manifest/README.md:35-56; dsh-app-boot/README.md:47-71;
dsh-client-modules/README.md:27-37.

- В package.json плагина: dsh.manifestVersion: 1, dsh.bundle.patch — один файл патча или
  упорядоченный список (относительно корня пакета), dsh.client = { platform: 'web' }, экспорт
  "./client"; dsh.client.external — не-базовые модули; dsh.client.inject — Client-сервисы, нужные
  плагину (S: dsh-package-manifest/README.md:35-56, dsh-client-modules/README.md:29-37).
- Bundle patch — YAML-строки композиции (insert/replace), которые launcher применяет отдельным
  слоем в порядке объявления (S: dsh-app-boot/README.md:51); пользовательские слои —
  <DSH_HOME>/profiles/<name>/cordis.patch.yml и home-патч (S: dsh-app-boot/README.md:64).
- Client-плагины исполняются в браузере; Host- и Client-половины — разные entry
  (./lib/index.js и ./lib/client.js) — S по package.json пакетов.

### 3.2 ctx.slots — регистрация компонентов — S

Подтверждённый паттерн (S: dsh-client-ui-action-cards/lib/client.js:855-870):

~~~js
ctx.slots.inject('tool.call.toolview', () => ctx.slots.register({
  name: 'tool.call.toolview',
  key: '<wire tool name>',        // key/id — зависит от слота
  locale: 'aiTerminal',           // опционально
  inject: (sessionId) => ({ ... }) // опционально: owner-props компонента
}, Component))
~~~

ctx.slots.inject(name, factory) оборачивает регистрацию в lifecycle слота; ctx.slots.register
возвращает disposer; компоненты получают standard props (в т.ч. inputActions) и owner-props
(S: dsh-client-ui-tool/README.md:39-49, dsh-client-ui-conversation/README.md:15,102).

### 3.3 Слоты, нужные AI-терминалу — S

| Слот | Для чего | Координата |
|---|---|---|
| tool.call.toolview | карточка инструмента в чате (key = wire-имя tool) | dsh-client-ui-tool/README.md:37-49; dsh-tools/README.md:99 |
| sidebar.right.pane.tab | тело вкладки правой панели («Терминал») | dsh-client-ui-sidebar-right/README.md:87 |
| sidebar.right.tab.menu.item | пункт меню вкладки | dsh-client-ui-sidebar-right/README.md:91 |
| conversation.session.header.actions | действия в шапке сессии | dsh-client-ui-conversation/lib/client.js:20896 (renderSlot) |
| conversation.session.header.utilities | утилиты шапки (пример — open-in-app) | dsh-client-ui-conversation/lib/client.js:20900; dsh-client-ui-open-in-app/README.md:59 |
| conversation.session.header.views.actions | пункты меню «More conversation views» | dsh-client-ui-conversation/README.md:88; lib/client.js:20747 |
| conversation.chat.turnTail | хвост turn'а в чате (карточки после ответа) | dsh-client-ui-chat/lib/client.js:9030,9333; dsh-client-ui-action-cards/README.md:43 |
| conversation.input.dock | док у поля ввода | dsh-client-ui-conversation/README.md:110 |
| conversation.composer, conversation.composer.bar, conversation.composer.dock, conversation.input.activity | слоты композера | dsh-client-ui-conversation/README.md:110 |
| contextMenu.icon, contextMenu.content | иконка/контент контекстного меню | dsh-client-ui-context-menu/README.md:49,51 |

Замечание S: conversation.session.header.actions **не описан в README** пакета, но слот
рендерится кодом (renderSlot("conversation.session.header.actions", {})) — факт подтверждён кодом,
не документацией.

### 3.4 insertQuote и вставка цитаты в поле ввода — S/I

**Координаты:** dsh-client-ui-conversation/README.md:65,102;
dsh-client-ui-conversation/lib/client.js:15340-15375.

- У per-session input facade есть action-face: actions = { insertQuote(text), captureInsertion(),
  insertText(text, span), setDraft(text), persistDraft(), ... }
  (S: lib/client.js:15340-15375).
- insertQuote(text) вставляет непустое выделение только в plain-режиме композера; отправка идёт
  обычным Markdown-текстом (S: dsh-client-ui-conversation/README.md:65).
- inputActions — stable standard prop, который ctx.uiSession.provide() выдаёт компонентам
  Conversation (S: dsh-client-ui-conversation/README.md:102; объявление props —
  lib/client.js:22778). **I:** план называет путь ctx.uiConversation.inputActions.insertQuote —
  прямого ctx.uiConversation.inputActions в коде не найдено; фактический доступ — standard prop
  inputActions в компоненте слота (проверить живым плагином).

### 3.5 Прочие Client-сервисы, нужные задачам 9–10

- ctx.contextMenus.register({ id, target, order, resolve }) — сервис Client-плагина для
  контекстных меню; useContextMenu().getTriggerProps(target, handlers?); слоты
  contextMenu.icon/contextMenu.content (S: dsh-client-ui-context-menu/README.md:29-55).
- ctx.uiConversation — registries conversation assembly/views, binding(), imageUrl(), userText
  (правила распознавания текста), interactions (жесты по выделению/контекстному меню)
  (S: dsh-client-ui-conversation/README.md:15,30-67). Пункт «Спросить о выделенном» логичнее делать
  через UiConversation.interactions + свой keyed slot (S: :67), а не через общий contextMenu.*.
- ctx.sidebarRight.openTab(kind, options?), openResource(address, options?), close, focus, split —
  навигация правой панели; тип вкладки регистрируется пакетом, тело — через sidebar.right.pane.tab
  (S: dsh-client-ui-sidebar-right/README.md:87,94-132).
- ctx.uiSession — Session-scope Hooks доступа (useAccess), whenAccess и provide() для
  conversation/input стандартных props (S: dsh-client-ui-session/README.md:28,
  dsh-client-ui-conversation/README.md:102).

## 4. Как это ложится на задачи 9–10

### Task 9 — инструменты терминала как инструменты Harness

- Каждый ToolInfo из Go (GET /api/agent/tools) регистрируется как
  ctx.tools.register(defineTool({...})); при рестарте бэкенда — перерегистрация (S: API выше).
- **Требуется конвертер JSON Schema → DSL** (dsh-tools/lib/index.js:59-228): Go отдаёт JSON-Schema
  (required: [...], без additionalProperties), а defineTool принимает «поле → value-schema» с
  булевым required и обязательным additionalProperties у объектов. Прямая передача сырой
  JSON Schema не подтверждена (I; для нетипизированных аргументов есть author-node json →
  annotation-only schema, S: dsh-tools/lib/index.js:165+).
- output.render — текстовый итог для модели; клиентская карточка — регистрация tool.call.toolview
  по wire-имени (terminal_<name>), owner-props: callId, toolName, phase, block, loadImage,
  openFile/inspect (S: dsh-client-ui-tool/README.md:39-49).
- exec.signal — обязательный мост отмены: fetch(..., {signal: exec.signal}) к Go-бэкенду
  (Review Focus 5 плана) (S: dsh-tools/README.md:129).
- presentCall/presentResult не использовать для Web-карточек (S: dsh-tools/README.md:99).

### Task 10 — один LLM, навыки, панели, MCP

- **LLM-мост:** ctx.llm.stream({provider, model, messages, tools?, temperature?, maxTokens?});
  OpenAI-запрос → durable Message[], ответ собирать из чанков (text-delta, tool-call),
  терминальный finish мапить в OpenAI finish_reason; tool_choice не поддерживается (S).
  Провайдер/модель по умолчанию — deepseek-official/deepseek-flash в Web
  (S: dsh-web-app/README.md:44).
- **Навыки:** ctx.skills.registerProvider((control) => ({name, list, get})), кеш инвалидируется
  control.invalidate(); локальные skills/trading-* можно отдавать файловым провайдером
  (<repo>/.dsh/skills, .agents/skills), чтобы работал и npx skills add (S: раздел 2.3).
- **Панель «Терминал»:** тип вкладки правой панели + тело через sidebar.right.pane.tab;
  навигация — ctx.sidebarRight.openTab (S: раздел 3.3).
- **«Отправить график в чат»:** кнопка в conversation.session.header.actions (S по коду) или
  conversation.session.header.utilities; внутри компонента — standard prop
  inputActions.insertQuote(text) (S метод; I путь доступа).
- **«Спросить о выделенном»:** ctx.uiConversation.interactions (selected-text, S) либо
  ctx.contextMenus.register (S: раздел 3.5).
- **MCP:** динамической регистрации сервера в рантайме нет. Сервер — строка композиции
  @deepseek-ai/dsh-mcp-client с config: {serverName, transport, url/command, headers}; строки
  объявляются в dsh.bundle.patch бандла (наш dsh-ai-terminal/cordis.patch.yml) или в профильном
  патче; правка строки перезагружает соединение (HMR — если включён)
  (S: dsh-mcp-client/README.md:31-54,90-94; dsh-app-boot/README.md:51,64,66).
  Экспорты пакета: Config, apply, createMcpToolDefinition, inject, name — API добавления сервера
  в рантайме нет (S: dsh-mcp-client/lib/index.js:837; I — интерпретация отсутствия).

## 5. Расхождения с планом (проверено)

| План говорит | Факт установленного Harness | Статус |
|---|---|---|
| ctx.tools.register(defineTool({… parameters, output:{schema, render}, execute(args, exec)})) | Подтверждено; parameters — DSL, не JSON Schema; у object-узлов обязателен additionalProperties; required — булев внутри поля | S |
| ctx.llm.stream({provider, model, messages, tools?, temperature?, maxTokens?}) | Подтверждено; tool_choice нет; ответ — сырые чанки + обязательный terminal finish; tools требуют toolHistory внутри agent-loop | S |
| ctx.skills.registerProvider(() => ({name, list(), get()})) | Фабрика получает control (signal, invalidate()), возвращает {name, list(), get()}; регистрация синхронная, возвращает disposer | S (уточнение) |
| ctx.workflowEngine.start({script, meta, args}) | Подтверждено; плюс parent, signal; run: id/meta/result/cancel(reason?)/dispose(), result не reject, dispose() обязателен | S |
| ctx.schedule.create({title, prompt, timing}) | Подтверждено; плюс target?, actorSessionId?, signal?; timing.kind — 7 видов | S |
| ctx.sessionController.createSession/prompt | Подтверждено для Web-композиции: сервис sessionController с createSession(request) и prompt(request, source); в Headless/Desktop может отсутствовать → альтернатива ctx.sessions + ctx.agents | S + I (сфера) |
| ctx.agents, ctx.storageDomain, ctx.subprocess, ctx.webServer.register | Подтверждено; детали — раздел 2 | S |
| Слоты: tool.call.toolview, sidebar.right.pane.tab, conversation.chat.turnTail, conversation.input.dock, contextMenu.* | Первые четыре подтверждены (в т.ч. кодом); contextMenu.* — это слоты contextMenu.icon/content, а сервис меню — ctx.contextMenus.register; для «спросить о выделенном» есть UiConversation.interactions | S/I |
| conversation.session.header.actions | Слот существует в коде (conversation/lib/client.js:20896), но не описан в README; рядом есть …header.utilities, …header.corner, …header.views.actions | S (код) / I (док) |
| ctx.uiConversation.inputActions.insertQuote | Метод insertQuote(text) есть в per-session action-face; наружу — standard prop inputActions (через ctx.uiSession.provide()); прямого ctx.uiConversation.inputActions не найдено | S метод / I путь |
| «Нет динамической регистрации MCP — только строки dsh.bundle.patch» | Подтверждено: MCP-серверы — только строки композиции (bundle patch / profile patch), runtime-API добавления нет; правка строки → reload (HMR) | S |
| (не в плане) env детей в ctx.subprocess | Дети **не наследуют** ambient-секреты: имена с KEY/PASSWORD/SECRET/TOKEN и DSH_* вычищаются, явный env мержится после scrub. Критично для TERMINAL_API_TOKEN/TRANSAQ_* | S |

## 6. Открытые вопросы (I)

1. Примет ли defineTool сырую JSON Schema (JsonSchemaNode) вместо DSL — в bundled JS виден только
   компилятор DSL; для Task 9 закладывать конвертер JSON Schema → DSL (обязательный
   additionalProperties, булев required).
2. Точный путь доступа к insertQuote из чужого Client-плагина: standard prop inputActions в
   компоненте слота подтверждён; ctx.uiConversation.inputActions — не подтверждён.
3. ctx.sessionController.createSession DTO (request.agentPreset, cwd, …) и возвращаемый тип в
   README не опубликованы; детали — по JSDoc в dsh-api-session-controller/lib/index.js.
4. Порядок слотов/owner-props у conversation.session.header.actions и conversation.chat.turnTail
   для нашего плагина нужно закрепить живым рендером.
5. HMR-перезагрузка строки MCP-клиента в Web-профиле (README: «editing the configuration entry
   reloads the server connection in place») — проверить на живом Harness.

## 7. Ключевые координаты (сводка)

| Что | Координата |
|---|---|
| Tool registry/defineTool | dsh-tools/README.md:29-99; dsh-tools/lib/index.js:59-360 |
| LLM stream/chunks | dsh-llm/README.md:48-96,180-184; dsh-llm/lib/index.js:985-1036 |
| Skills registry | dsh-skill/README.md:29-69; dsh-skill/lib/index.js:120-190 |
| Skill dirs | dsh-skill-filesystem/README.md:35-58 |
| Workflow engine | dsh-workflow/README.md:31-58 |
| Schedule | dsh-schedule/README.md:50-83 |
| Session controller | dsh-api-session-controller/lib/index.js:235,831,3791; dsh-schedule/README.md:54 |
| Agents | dsh-agent/README.md:29-79 |
| Storage domain | dsh-storage-domain/README.md:27-74 |
| Subprocess + env scrub | dsh-subprocess/README.md:38-96 (особенно :79-85); dsh-subprocess-local/lib/runner-launch-*.js (childEnv) |
| Web server | dsh-host-webserver/README.md:27-71 |
| Sessions | dsh-session/README.md:27-89 |
| Манифест/bundle patch | dsh-package-manifest/README.md:35-56; dsh-app-boot/README.md:47-71 |
| Client modules/manifest | dsh-client-modules/README.md:27-37; dsh-package-manifest/README.md:33-47 |
| tool.call.toolview | dsh-client-ui-tool/README.md:37-49; dsh-client-ui-action-cards/lib/client.js:864-870 |
| sidebar.right.pane.tab | dsh-client-ui-sidebar-right/README.md:87 (меню :91, навигация :94-132) |
| conversation.session.header.actions | dsh-client-ui-conversation/lib/client.js:20896 (соседние :20747,:20900) |
| conversation.chat.turnTail | dsh-client-ui-chat/lib/client.js:9030,9333; dsh-client-ui-action-cards/lib/client.js:860-865 |
| conversation.input.dock | dsh-client-ui-conversation/README.md:110 |
| insertQuote/inputActions | dsh-client-ui-conversation/README.md:65,102; lib/client.js:15340-15375,22778 |
| context menus | dsh-client-ui-context-menu/README.md:29-55 |
| MCP client (строки) | dsh-mcp-client/README.md:31-54,90-94; dsh-mcp-client/lib/index.js:837 |
| Web-композиция/провайдер по умолчанию | dsh-web-app/README.md:44,48-61,92 |

## 8. Что не проверялось

- Живой запуск плагина и Client-слотов в Harness (нужен реальный плагин; документ — только
  статическая верификация по установленной сборке).
- Remote-вызовы (ctx.remote.*) и точные JSON-формы DTO — в README описаны частично; в asar нет
  *.d.ts.
- Полные страницы docs/subsystems/* (в asar отсутствуют), на которые ссылаются README.
- Поведение провайдеров LLM с реальным ключом (только контракт сервиса).
