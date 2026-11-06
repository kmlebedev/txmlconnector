# Исследования терминала: трендовые стратегии, walk-forward, deflated Sharpe и PBO

Документ описывает исследовательский контур ИИ-терминала: трендовые стратегии каталога,
walk-forward проверку с честным out-of-sample, поправку на множественное тестирование
(deflated Sharpe) и вероятность переобучения (PBO). Обновляется задачами 15–18 плана
[2026-11-05-ai-native-terminal](../superpowers/plans/2026-11-05-ai-native-terminal.md).

Статусы утверждений: **S** — проверено на демо-сервере, **O** — оракул DLL, **D** — декомпилятор,
**I** — вывод/реализация в коде. Ниже все числа и формулы — **I**.

## 1. Трендовые стратегии каталога (задача 15)

| ID | Логика | Параметры (default) |
|---|---|---|
| `trend_tsmom` | Вход, когда доходность за `lookback` баров выше порога; выход — при отрицательной доходности | `lookback` (20), `thresholdPct` (0) |
| `trend_keltner` | Канал EMA ± `multiplier`·ATR; вход выше верхней границы, выход ниже средней линии | `period` (20), `multiplier` (2) |
| `trend_supertrend` | Полосы hl2 ± mult·ATR Уайлдера с рекурсивным трендом; вход на развороте вверх, выход на развороте вниз | `period` (10), `multiplier` (3) |
| `trend_maribbon` | Лента из пяти SMA между fast и slow; вход при полном порядке fast→slow, выход при обратном | `fast` (5), `slow` (40) |

Свойства (все **I**, проверены тестами):

- Сигнал принимается на закрытии бара i, исполняется движком по открытию бара i+1; заглядывания
  вперёд нет — значение на баре i зависит только от `bars[0..i]` (`TestTrendStrategiesNoLookahead`).
- На синтетическом тренде с откатами стратегии совершают сделки, на флэте — не более одной
  (`TestTrendStrategiesOnTrendAndFlat`).
- Параметры нормализуются как у остальных стратегий каталога: отсутствующие значения — default,
  выходящие за границы — clamp, зависимые (fast < slow) поддерживаются согласованными.

## 2. Walk-forward (задача 15)

`research.WalkForward(ctx, req)` повторяет схему rolling walk-forward:

1. Бары делятся на окна `trainBars` и следующие за ними `testBars`; окно сдвигается на
   `stepBars` (0 → `testBars`).
2. Для каждого окна перебирается сетка параметров `grid` (полное декартово произведение,
   не более 1000 комбинаций) — параллельно, не более `runtime.GOMAXPROCS` задач.
3. Лучшая комбинация выбирается по in-sample Sharpe, затем по доходности, числу сделок и
   просадке (детерминированный tie-break).
4. Выбранные параметры прогоняются на следующем (out-of-sample) окне; OOS-результаты всех
   фолдов сшиваются в одну кривую доходности, по ней считаются агрегированные метрики
   `WalkForwardResult.OOS`, по train-окнам — `IS`.

Важно: каждый фолд видит только свои train+test бары — окна передаются в движок срезом, а бары
за пределами окна недоступны. Это закреплено `TestWalkForwardNoLookahead`: метрики фолда
совпадают с отдельным бэктестом на том же срезе, а «отравление» баров после окна не меняет фолд.

Ограничения и допущения (**I**):

- Период баров выводится из расстояния между первыми барами ряда (в запросе периода нет).
- Комиссия/проскальзывание — как в движке: 0 означает значения по умолчанию 5/1 б.п.
- Число фолдов = `(len(bars) - trainBars - testBars)/stepBars + 1`; меньше одного фолда — ошибка.
- Окна релизов макростатистики: `WalkForwardRequest.Events []vintage.Release` и
  `ExcludeEventWindow` (интервал `[At, At+window]` после каждого релиза, `research.EventWindows`).
  Бары, попавшие в окно, исключаются и из train-, и из test-окон фолда — это позволяет сравнить
  прогон «с окнами релизов» и «без них». Инстанты — UTC; релиз 8:30 ET 10.03.2026 попадает в
  окно 15:30 МСК (летнее время США), 03.11.2026 — в 16:30 МСК (зимнее) — `TestEventWindowAcrossUSDST`.
- `WalkForwardRequest.Run` — seam для подмены движка (тесты, будущий replay); nil = `backtest.Run`.

## 3. Deflated Sharpe (задача 15)

Метрика Bailey & López de Prado (2014) отвечает на вопрос: «насколько вероятно, что лучший
Sharpe из N испытаний — настоящая находка, а не максимум шума?».

- `research.MomentsOf(returns)` — среднее, стандартное отклонение, Sharpe (на одно наблюдение,
  без аннуализации), асимметрия и эксцесс (raw kurtosis, у нормального распределения 3).
- `research.DeflatedSharpe(sr, trials, observations, skewness, kurtosis)`:
  ожидаемый максимум Sharpe под нулевой гипотезой оценивается по дисперсии оценки Sharpe
  `1/(observations-1)` и числу испытаний (формула с постоянной Эйлера–Маскерони);
  результат — вероятность в [0, 1], что наблюдённый Sharpe выше этого максимума.

  В `WalkForwardResult.DeflatedSharpe` число испытаний = размер сетки, Sharpe и моменты
  считаются по сшитым OOS-доходностям.

- Тест `TestDeflatedSharpePenalizesTrials` фиксирует эталонные значения (независимая
  реализация формулы, допуск 1e-9) и монотонность: при том же Sharpe больше испытаний —
  меньше уверенности.

## 4. PBO — вероятность переобучения (задача 15)

`research.PBO(trialReturns, blocks)` — CSCV (combinatorial symmetric cross-validation,
Bailey, Borwein, López de Prado & Zhu):

- OOS-доходности всех испытаний сетки режутся на `blocks` непрерывных блоков (по умолчанию 16,
  принудительно чётное);
- для каждой комбинации половины блоков «in-sample» выбирается лучшая стратегия;
- её ранг в оставшейся половине («out-of-sample») считается переобучением, если попал в нижнюю
  половину списка испытаний;
- PBO = доля таких комбинаций: 0 — in-sample победитель держится и вне выборки, 1 — всегда
  переобучается. При недостатке данных возвращается 0.5 («не определено»).

В `WalkForwardResult.PBO` матрица испытаний — это OOS-доходности каждой комбинации сетки,
сшитые по фолдам. Тест `TestPBOFlagsOverfitting`: устойчивое преимущество не штрафуется (PBO=0),
а «победитель первой половины выборки, проигравший вторую» помечается (> 0.4).

Планируемая политика продвижения SKILL-паттернов (задача 19) использует пару этих метрик:
`MinDeflatedSharpe = 0.95`, `MaxPBO = 0.3`, не менее 30 OOS-сделок и 3 месяцев OOS.

## 5. Проверка

Команды проверки задач 15:

```bash
go test -race ./terminal/backtest/... ./terminal/research/...
make verify
```

Ключевые тесты: `TestTrendStrategiesOnTrendAndFlat`, `TestTrendStrategiesNoLookahead`,
`TestWalkForwardNoLookahead`, `TestWalkForwardGridAndAggregates`,
`TestEventWindowAcrossUSDST` (Review Focus 2 — переход США на летнее/зимнее время),
`TestWalkForwardExcludesEventWindows`, `TestDeflatedSharpePenalizesTrials`,
`TestPBOFlagsOverfitting`.

## 6. Ограничения и известные проблемы

- Метрики агрегата walk-forward дублируют формулы движка (популяционная дисперсия, годовая
  аннуализация Sharpe, capped profit factor) — это осознанное дублирование: движок не отдаёт
  готовую сшитую кривую.
- Выбор параметров идёт по in-sample Sharpe; сравнение стратегий в агенте ранжирует по OOS:
  инструменты подключены давно — tool-bridge T9/T10, walk-forward и batch с T15/T24, компактные
  ответы и `artifactId` с T18; контракт — `GET /api/agent/tools` (§8).
- Живых прогонов на истории TRANSAQ в этой задаче не было: тесты синтетические и на фикстурах
  (`I`), ручная проверка промптом в DSH — после появления tool-bridge (задача 9).
- Fingerprint-исследование (задача 17) документируется отдельно в `docs/ai-terminal/fingerprint.md`.

## 7. Оракул волатильности: релизы и новости (задача 16)

Пакет [`terminal/volatility`](../../terminal/volatility/oracle.go) — статическая модель ожидаемой
волатильности без внешних библиотек: HAR-RV по внутридневным барам, окна релизов макростатистики и
затухающая интенсивность аннотированных новостей. Все формулы и числа — **I** (реализация и
синтетические тесты; живых прогонов на истории TRANSAQ в этой задаче не было).

### 7.1 Контракт

```go
type Forecast struct { Symbol string; At time.Time; Horizon time.Duration; SigmaPct float64; Multiplier float64; Drivers []Driver; Regime string }
type Driver struct { Kind string; Ref string; Contribution float64 }
func Fit(ctx context.Context, bars []backtest.Bar, rel []vintage.Release, news []vintage.NewsItem, asOf time.Time) (*Model, error)
func (m *Model) Forecast(at time.Time, horizon time.Duration, upcoming []vintage.Release, recentNews []vintage.NewsItem) Forecast
func (m *Model) QLIKE(bars []backtest.Bar, rel []vintage.Release, news []vintage.NewsItem, from, to time.Time) (float64, int)
```

- `SigmaPct` — ожидаемое стандартное отклонение доходности за горизонт, %; `Multiplier` —
  отношение прогнозной дисперсии к HAR-базовой (без релизов и новостей): ×3 означает втрое большую
  дисперсию. Тест `TestOracleRaisesSigmaBeforeKeyRateDecision` строит синтетику с ×16 по дисперсии
  вокруг решения ЦБ и требует `Multiplier > 2` в окне решения.
- `Regime`: `calm` (Multiplier ≤ 0.85), `normal`, `event` (≥ 1.3 или активен релиз/новость),
  `stress` (≥ 2.0).
- `Drivers`: `release:<категория>`, `news:intensity`, `har:daily|weekly|monthly` — вклады в
  лог-дисперсию для объяснения прогноза агенту и UI.

### 7.2 Модель

1. **Реализованная дисперсия дня** — сумма квадратов внутридневных доходностей баров дня
   (овернайт-гэп не входит); неполные дни (< 20 баров) отбрасываются.
2. **HAR-RV**: `ln RV_t = c + βd·ln RV_{t−1} + βw·ln mean(RV, 5 дней) + βm·ln mean(RV, 22 дня) + …`;
   OLS решается вручную (нормальные уравнения + ridge 1e-6, метод Гаусса с выбором главного
   элемента) — без внешних библиотек.
3. **Окна релизов**: дамми по категории, если окно `[At−2ч, At+2ч]` пересекает день. Календарь
   известен заранее, поэтому в обучение попадают релизы до `asOf`.
4. **Новости**: `intensity = Σ exp(−(t_day−t_news)/24ч)·(1+|sentiment|)` по новостям моложе 72 ч;
   регрессор `ln(1+intensity)`. И положительная, и отрицательная тональность повышают
   неопределённость, поэтому вес — модуль тональности.
5. **Point-in-time**: `Fit` берёт только бары/релизы/новости строго до `asOf` и только полные
   торговые дни; `TestOracleFitUsesOnlyDataBeforeAsOf` «отравляет» данные после `asOf` (σ×20) —
   коэффициенты и прогноз не меняются.
6. **Прогноз**: HAR-состояние последнего полного дня + активные окна релизов в `[at, at+horizon]`
   + интенсивность новостей до `at`; σ горизонта = `sqrt(exp(lnRV)/N_bar · horizon/step) · 100%`
   (внутридневная дисперсия считается равномерной по барам дня).
7. **Качество**: QLIKE = `RV_true/RV_pred − ln(RV_true/RV_pred) − 1` по дням после обучения;
   `TestQLIKEBeatsHARonSynthetic` требует, чтобы оракул был лучше наивного HAR-only (без релизов и
   новостей) на синтетике с эффектом решения ЦБ.

### 7.3 Инструменты агента и график

- `vol_forecast(symbol, periodSec?, count?, horizon?, asOf)` — обучает модель на истории
  (по умолчанию 15-минутные бары — `periodSec=900`, допустимы 300/900/3600, до 5000 баров),
  берёт релизы и новости из `vintage.Store`, возвращает компактную сводку (σ, множитель, режим,
  ≤6 драйверов, QLIKE за последние 30 дней) и `Artifact{Kind:"volatility"}` для UI.
  **`asOf` обязателен** (T18): без него — ошибка аргумента, обучение видит только данные до `asOf`;
  `horizon` по умолчанию 8h. С окном `since` относительно `asOf` работает `get_news` (события
  позже `asOf` не видны).
- `vol_events(from, to, categories?, asOf)` — релизы календаря и аннотированные новости окна
  `[from, to]`; **`asOf` обязателен** и не может быть раньше `to` (point-in-time: релизы/новости
  позже `asOf` не возвращаются). Ответ компактный, полный календарь — в
  `Artifact{Kind:"vol_events"}`, на который ссылается `artifactId`.
- Винтажный источник подключается необязательным интерфейсом `VolatilityDataSource`
  (реализация `vintage.Store` в адаптере данных). Пока адаптер не связан с ClickHouse, инструменты
  возвращают понятную ошибку «источник не подключён».
- График: `CandleChart.setVolatility(payload)` в `terminal/web/chart.js` рисует конус ожидаемой
  волатильности от последней цены (расширение ∝ √времени до `sigmaPct` на горизонте, цвет по
  режиму), вертикальные маркеры релизов (будущие прижаты к правому краю) и чип
  `σ X% горизонт ×multiplier regime`. Контракт `volatility`/`vol_events` — для проводки артефактов
  в `app.js`/`ai.js` (отдельная задача).

### 7.4 Ограничения и не проверенное

- Живых прогонов оракула на TRANSAQ не было: тесты синтетические, ClickHouse не поднимался (**I**).
- Для обучения нужно ≥ 62 полных торговых дней внутридневных баров (22 дня warm-up HAR + 40
  обучающих). 5-минутные бары в лимите истории фида (5000) дают ~17 дней, поэтому по умолчанию
  инструмент берёт 15-минутные (~150 торговых дней) и сообщает об ошибке, если данных мало.
- Пороги режимов (0.85 / 1.3 / 2.0) и трактовка коэффициентов — эмпирические (**I**), уточняются
  на реальных данных; событийные дамми требуют нескольких однотипных релизов в обучении.
- Модель статическая (OLS); интерфейс `Model` позволяет заменить её ML-моделью (задача 20).

Проверки задачи 16:

```bash
go test -race ./terminal/volatility
go test -race ./terminal/agent -run 'Vol|ResearchTools|WalkForward'  # проводка инструментов
node --check terminal/web/chart.js
make verify
```

Ключевые тесты: `TestOracleRaisesSigmaBeforeKeyRateDecision`, `TestOracleFitUsesOnlyDataBeforeAsOf`,
`TestQLIKEBeatsHARonSynthetic`.


## 8. Компактные ответы и point-in-time (задача 18)

Все research-инструменты агента (`walk_forward`, `vol_forecast`, `vol_events`,
`detect_execution_algos`, `lob_backtest`, `get_news`) с T18 работают по одному контракту:

- **`asOf` обязателен** — момент, на который известны данные (point-in-time); без него инструмент
  возвращает ошибку аргумента с подсказкой. У research-запроса нет неявного «сейчас», иначе ответ
  зависел бы от часов. `get_news` фильтрует по `first_seen <= asOf`, а окно `since` отсчитывается
  от `asOf`, а не от текущего времени.
- **Компактный ответ модели + `artifactId`** — в ход LLM попадает сжатая сводка (не более 2 КБ,
  `TestToolResultCompactUnder2KB`): счётчики и первые несколько строк, а полный результат
  (equity/fills/отпечатки/календарь) хранится в bounded in-process store (64 артефакта) под
  `artifactId`. UI получает полный payload отдельным `Artifact`-ом.
- Артефакты: `walk_forward`, `volatility`, `vol_events`, `execution_algos`, `lob_backtest`,
  `news`; их `Kind` и поля — контракт UI/графика.
- Времена `asOf`/`from`/`to` — RFC3339 UTC; ошибки аргументов — `errBadArgument` (модель получает
  подсказку, а не 500).

Проверки T18:

```bash
go test -race ./terminal/agent -run 'ResearchTools|ToolResultCompact|PointInTime'
```


## 9. Живая проверка оракула на реальных макро/новостных данных (S, 2026-11-06)

Бэктест-бары для проверки синтетические (живой intraday-источник — только демо-сервер и не в
unit-тестах), но релизы и новости взяты из живых point-in-time-витрин `clickhouse-import-rosstat`
(см. `data.md` §8):

- `TXML_IT_CLICKHOUSE=1 TXML_CLICKHOUSE_URL=http://localhost:8123 go test ./terminal/volatility -run Live -v`
  → `TestLiveOracleFitWithRealMacro` PASS: 90 синтетических торговых дней (по 30 десятиминутных баров),
  ~276 релизов из живого календаря и 87 новостей за 7 дней; `Fit` строит модель, `Forecast` даёт
  конечные `SigmaPct`/`Multiplier`, режим из контракта и объяснимые `Drivers`.
- Это подтверждает путь «сырые витрины → `vintage.Store` → `volatility.Fit/Forecast`» на живых
  макро/новостных данных (S); качество прогноза (QLIKE против наивного HAR) на живых барах остаётся
  **I** — нужен живой intraday-ряд демо-сервера.
- Тест помечен env-гейтом `TXML_IT_CLICKHOUSE=1`; без него — `t.Skip`.