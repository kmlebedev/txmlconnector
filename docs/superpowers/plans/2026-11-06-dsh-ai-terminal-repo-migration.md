# План миграции `dsh-ai-terminal` в отдельный репозиторий `dsh-trading-transaq`

Дата: 2026-11-06. Статус: **выполняется** (Lead: DeepSeek Harness; RTC-команда).
Источник: `github.com/kmlebedev/txmlconnector`, ветка `main`, commit `dd38d93` (2026-11-06).
Цель: `github.com/kmlebedev/dsh-trading-transaq` (публичный, пустой на момент старта).

## 1. Контекст (проверено в дереве)

| Факт | Значение |
|---|---|
| Плагин | `dsh-ai-terminal/` — ESM-пакет `@local/dsh-ai-terminal` 0.1.0, 30 tracked-файлов, 352 KB |
| Тесты плагина | `node --test dsh-ai-terminal/test/` → **54/54 pass** (Node v26.12.0), backend не нужен |
| История каталога | 16 коммитов (2026-11-05 … 2026-11-06), последний `d55b9eb` |
| Go-бэкенд | `cmd/terminal` + `terminal/` (122 Go-файла) + `native/`; **остаётся в txmlconnector** |
| Навыки | `skills/trading-*` — плагин отдаёт их провайдером из `<repoDir>/skills`; остаются в txmlconnector |
| Доки | `docs/ai-terminal/` (ARCHITECTURE, PROGRESS, dsh-integration, скриншоты) — остаются в txmlconnector |
| Установка сегодня | профиль `~/.dsh/profiles/desktop`: `dependencies["@local/dsh-ai-terminal"] = link:<txmlconnector>/dsh-ai-terminal`, эта запись есть в `dsh.profile.bundles`, строка патча `- id: ai-terminal` |
| Поиск бэкенда | `index.js findRepoDir()`: config.repoDir → walk-up от каталога плагина до `cmd/terminal/main.go` (6 уровней) → родитель плагина |
| Скриншоты README | `dsh-ai-terminal/README.md` ссылается на `../docs/ai-terminal/screenshot-*.png` (вне каталога пакета) |
| Целевой репозиторий | создан, `isEmpty: true`, `visibility: PUBLIC`, default branch не создан |
| gh CLI | авторизован (`kmlebedev`, scope `repo`), push доступен |

**Внешние условия, влияющие на миграцию**

1. **PR #66** `fix/nightly-workflow-evidence` (1 коммит, CI зелёный) меняет 5 файлов внутри
   `dsh-ai-terminal/`; после удаления каталога PR станет неактуальным.
2. **Профиль DSH** — внешний конфиг `~/.dsh`; правится только с явного согласия пользователя,
   применение требует рестарта DSH (рестарт завершает текущую сессию).
3. **LICENSE** в txmlconnector отсутствует; выбор лицензии нового публичного репозитория — решение
   владельца.

## 2. Решение (базовый вариант плана)

- **Объём**: переносится только JS-пакет `dsh-ai-terminal`. Go-бэкенд, `skills/`, `docs/ai-terminal/`
  остаются в txmlconnector (единый источник терминала и протокола). Новый репозиторий — дом для
  DSH-поверхности; будущие пакеты (`dshtrading-connector-transaq` и др.) могут добавляться рядом.
- **Раскладка**: monorepo-стиль — пакет сохраняет путь `dsh-ai-terminal/` внутри нового репозитория.
  Это сохраняет имена, относительные импорты и тесты, а также оставляет место соседним пакетам.
- **История**: сохраняется (filter-branch по пути `dsh-ai-terminal` в отдельном клоне; 16 коммитов,
  сообщения и даты автора). Сверху — 2 собственных коммита: scaffolding и split-support.
- **Имя пакета**: `@local/dsh-ai-terminal` и row id `ai-terminal` **не переименовываются** (меньше
  риска для профиля и client-slot записей); repo name `dsh-trading-transaq` — имя продукта.
- **Cutover профиля**: в этом проходе **не выполняется** (нужно согласие + рестарт DSH). Готовится
  пошаговая инструкция и rollback; до cutover старая копия в txmlconnector остаётся рабочей.

**Открытые решения владельца** (зафиксированы, не блокируют создание репозитория):

- [ ] Лицензия нового репозитория: MIT / Apache-2.0 / без лицензии.
- [ ] Cutover профиля `~/.dsh` на новый checkout (и когда) + рестарт DSH.
- [ ] Судьба PR #66: влить в main до удаления каталога, либо портировать diff в новый репозиторий.
- [ ] Локальный клон `~/GolandProjects/dsh-trading-transaq` (вне workspace DSH — нужен approve).

## 3. Целевая структура нового репозитория

```text
dsh-trading-transaq/
├── .github/workflows/node-test.yml   # node --test dsh-ai-terminal/test/ (Node 24, ubuntu)
├── .gitignore                        # node_modules/, .DS_Store, .cache/, *.log, *.env
├── AGENTS.md                         # правила работы агентов в этом репозитории
├── LICENSE                           # отложено: решение владельца (см. §2)
├── README.md                         # что это, состав, быстрый старт, бэкенд txmlconnector
├── Makefile                          # test / smoke
├── docs/
│   ├── migration.md                  # происхождение, решения, как собран репозиторий
│   ├── screenshot-main.png           # из docs/ai-terminal/ (ссылки в README плагина)
│   └── screenshot-dsh-embedded.png
└── dsh-ai-terminal/                  # плагин: история + текущие файлы
    ├── package.json                  # + repository/homepage/engines (license — по решению)
    ├── index.js                      # findRepoDir: config → env → walk-up → sibling
    ├── README.md                     # split-установка, repoDir/env, бэкенд txmlconnector
    ├── cordis.patch.yml              # комментарии о repoDir по умолчанию
    ├── test/repo-dir.test.mjs        # новый: порядок поиска checkout бэкенда
    └── … (client.js, tools.js, autonomy.js, workflows/, locale/, icon.svg, тесты)
```

## 4. Задачи нового репозитория

### T1. Извлечение истории и каркас (owner: repo-migrator)

1. Клон-песочница: `git clone --no-local /Users/whitefox/GolandProjects/txmlconnector <staging>/src`
   (внутри `.re-run/migration-dsh-trading-transaq/`, каталог gitignored).
2. `git filter-branch --prune-empty -- dsh-ai-terminal` → история только по пути плагина,
   пути сохранены; проверить: 16 коммитов, `git log --stat` показывает только файлы плагина.
3. Переименовать ветку в `main`, удалить remote/refs исходного репозитория, создать коммит
   `chore(repo): scaffold the dsh-trading-transaq repository` со scaffolding из §3
   (README/AGENTS/.gitignore/.github/Makefile/docs/migration.md, скриншоты).
4. Коммит `feat(plugin): resolve the terminal checkout after the repository split`:
   - `findRepoDir` (index.js): порядок `config.repoDir` → `DSH_AI_TERMINAL_REPO_DIR` → walk-up →
     соседний checkout `../txmlconnector` → fallback; экспортируемый чистый помощник для тестов;
   - `test/repo-dir.test.mjs` (walk-up, env, sibling, configured, fallback);
   - `package.json`: `repository`, `homepage`, `engines.node >= 24`, `files` — без изменений;
   - README: установка из нового репозитория, требование checkout `txmlconnector` (рядом или
     `repoDir`/`binaryPath`), актуальные ссылки на скриншоты;
   - `cordis.patch.yml`: комментарии про `repoDir`/`DSH_AI_TERMINAL_REPO_DIR`.
5. `node --test dsh-ai-terminal/test/` → все зелёные (54 + новые repo-dir).
6. Отчёт Lead: SHA коммитов, вывод тестов, `git log --oneline`, `git ls-files` count,
   `npm pack --dry-run` (или `npm pack` в /tmp) — пакет не теряет runtime-модули.

**Acceptance**: staging-репозиторий готов к push, тесты зелёные, история сохранена, секретов нет.

### T2. Перенос ссылок в txmlconnector (owner: source-cleanup; отдельный worktree)

Ветка `migration/dsh-trading-transaq` от `dd38d93` в отдельном worktree
(`<staging>/cleanup`), основной checkout не трогается. Обновить:

| Файл | Изменение |
|---|---|
| `README.md` | ссылка `dsh-ai-terminal/README.md` → URL нового репозитория; пометка «плагин вынесен» |
| `docs/ai-terminal/README.md` | раздел «Встраивание в DeepSeek Harness» → ссылка на новый репозиторий + `repoDir` |
| `docs/ai-terminal/PROGRESS.md` | баннер в начале: плагин вынесен 2026-11-06 в `kmlebedev/dsh-trading-transaq` (история ниже — как было) |
| `docs/ai-terminal/dsh-integration.md` | баннер: пути `dsh-ai-terminal/*` — исторические, код теперь в новом репозитории |
| `docs/ai-terminal/adr-dsh-trading.md` | строка статуса: решение D реализовано выносом плагина |
| `tools/dsh-plugin-enable.sh` | link на `${DSH_AI_TERMINAL_DIR:-$HOME/GolandProjects/dsh-trading-transaq/dsh-ai-terminal}` |
| `tools/dsh-crash-bundle.sh` | путь плагина — новый checkout (и старый путь как fallback) |
| `skills/README.md`, `skills/trading-terminal/SKILL.md` | «плагин `dsh-ai-terminal` из репозитория `dsh-trading-transaq`» |

**Acceptance**: `git grep -n dsh-ai-terminal` не содержит рабочих ссылок на удалённый каталог
(исторические упоминания в PROGRESS/dsh-integration допустимы и помечены); ссылки не битые.

### T3. Удаление каталога из txmlconnector (owner: source-cleanup; после T1 и T2)

1. В том же worktree: `git rm -r dsh-ai-terminal`.
2. Коммит `refactor(dsh-plugin): move the AI terminal plugin to kmlebedev/dsh-trading-transaq`
   (в тело — ссылка на новый репозиторий, provenance commit `dd38d93`).
3. `gofmt -l`, `go vet ./...`, `go build ./...`, кросс-сборки `CGO_ENABLED=0`, `go test -race ./...`
   (или `make verify` с `GOCACHE="$PWD/.cache/go-build"`) → exit 0.
4. Отчёт Lead: `git diff --stat main...HEAD`, вывод проверок.

**Acceptance**: ветка готова к PR, Go-проверки зелёные, основной checkout и незакоммиченные файлы
пользователя (`docs/ai-terminal/adr-*`, `docs/superpowers/plans/2026-11-06-dsh-trading-transaq-connector.md`,
скриншоты) не затронуты.

### T4. Публикация и cutover (owner: Lead, после T1)

1. `git remote add origin https://github.com/kmlebedev/dsh-trading-transaq.git`, push `main`.
2. Проверка: `gh repo view`, дерево файлов, run CI `node-test`; README на GitHub рендерится.
3. Cutover-инструкция в `README.md`/плане: клон в `~/GolandProjects/dsh-trading-transaq`,
   `~/.dsh/profiles/desktop/package.json` link, `cordis.patch.yml` строка `ai-terminal` config
   `repoDir: ~/GolandProjects/txmlconnector`, `dsh plugin --profile desktop install`, рестарт DSH,
   smoke (иконка, панель, health), rollback (link назад на txmlconnector + `git archive` из истории).

### T5. Независимая проверка (owner: verifier)

- Новый репозиторий: тесты, история, отсутствие секретов/абсолютных путей `/Users/`, ссылки README,
  `package.json files`/exports, YAML workflow.
- txmlconnector ветка: отсутствие рабочих ссылок на удалённый каталог, Go-проверки, отсутствие
  случайных файлов в diff.
- Кросс-проверка плана §1–§4 с фактическим результатом; отчёт P1/P2/P3.

## 5. Проверки (DoD)

- [ ] Новый репозиторий: `node --test dsh-ai-terminal/test/` зелёный; история 16+ коммитов; CI
      workflow валиден; `git grep -nE 'TC_PASSWORD|TC_LOGIN' ` — без значений (только `os.Getenv`-стиль
      документации, без секретов); нет `.env`, нет абсолютных путей пользователя.
- [ ] `README.md` плагина: инструкция установки из нового репозитория + бэкенд checkout.
- [ ] txmlconnector: Go-проверки зелёные; `git grep` по удалённому пути — только исторические
      баннеры; основной checkout не тронут.
- [ ] GitHub: push выполнен, `main` — default branch, CI run зелёный (или зафиксировано «не проверено»).
- [ ] Cutover: **не выполнен** без согласия; инструкция + rollback в отчёте.

## 6. Риски и откат

| Риск | Митигация |
|---|---|
| PR #66 конфликтует/теряется | не мержим ничего; diff #66 при необходимости портируется в новый репо или PR вливается до удаления |
| Сломанный boot DSH после cutover | cutover только после успешного push и проверки staging; rollback — link назад + `git archive dd38d93 -- dsh-ai-terminal` |
| Потеря относительных ссылок README | скриншоты копируются в `docs/`, ссылки проверяются verifier-ом |
| filter-branch искажает историю | сверка `git log --stat` до/после по 16 коммитам, provenance commit в `docs/migration.md` |
| Секреты/абсолютные пути | grep-проверка в T5, `*.env` в .gitignore |
| Cutover без согласия | профиль `~/.dsh` не редактируется в этом проходе |

## 7. Порядок и владельцы

```text
T1 repo-migrator ──▶ T4 Lead (push, проверка) ──▶ T5 verifier
T2 source-cleanup (параллельно T1) ──▶ T3 source-cleanup (после T1+T2) ──▶ T5
```
