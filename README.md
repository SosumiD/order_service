# Backend-сервис заказов на Go

Готовое решение текущего контроля: REST API для заказов с хранением данных в памяти, валидацией, переходами статусов, историей, тестами и Swagger/OpenAPI

## Запуск

Требуется Go 1.22 или новее. Сторонние Go-библиотеки не используются

```bash
go run ./cmd/api
```

Сервер запускается на `http://localhost:8080`. Адрес можно изменить через переменную окружения `HTTP_ADDR`

- Swagger UI: `http://localhost:8080/swagger/`
- OpenAPI JSON: `http://localhost:8080/swagger/openapi.json`
- Проверка сервиса: `http://localhost:8080/health`

Для отображения Swagger UI нужен доступ браузера в интернет, поскольку JavaScript и CSS интерфейса загружаются с CDN. Спецификация OpenAPI встроена в приложение и доступна без интернета

## API

| Метод | Путь | Результат |
|---|---|---|
| `GET` | `/health` | `200`, состояние сервиса |
| `POST` | `/api/v1/orders` | `201`, созданный заказ |
| `GET` | `/api/v1/orders` | `200`, список заказов |
| `GET` | `/api/v1/orders/{id}` | `200`, заказ |
| `PATCH` | `/api/v1/orders/{id}/status` | `200`, обновлённый заказ |
| `GET` | `/api/v1/orders/{id}/history` | `200`, история изменений |

Создать заказ:

```bash
curl -X POST http://localhost:8080/api/v1/orders \
  -H "Content-Type: application/json" \
  -d '{"items":[{"name":"Keyboard","quantity":2,"price":500000}]}'
```

Ответ:

```json
{
  "id": 1,
  "items": [{"name": "Keyboard", "quantity": 2, "price": 500000}],
  "total_price": 1000000,
  "status": "new",
  "created_at": "2026-11-27T10:00:00Z"
}
```

Изменить статус:

```bash
curl -X PATCH http://localhost:8080/api/v1/orders/1/status \
  -H "Content-Type: application/json" \
  -d '{"status":"processing"}'
```

Получить историю:

```bash
curl http://localhost:8080/api/v1/orders/1/history
```

Цены указываются целыми числами в минимальных денежных единицах. Сумма рассчитывается как `sum(price * quantity)`. Позиции должны быть непустыми, `name` — непустой строкой, `quantity` и `price` — положительными числами

## Статусы

Разрешены только переходы:

```text
new ──→ processing ──→ completed
  │           │
  └──→ cancelled ←────┘
```

Новый заказ получает статус `new`. Каждое изменение записывается в историю вместе со временем. `completed` и `cancelled` — конечные статусы

## Ошибки

Все предметные ошибки возвращаются в JSON:

```json
{"error":"status transition is not allowed","code":"INVALID_STATUS_TRANSITION"}
```

| HTTP | Случай |
|---|---|
| `400` | Неверный JSON, ID, заказ или статус |
| `404` | Заказ отсутствует |
| `409` | Недопустимый переход статуса |
| `500` | Внутренняя ошибка |

В ответах `500` внутренние подробности не раскрываются

## Структура

```text
cmd/api/main.go                       запуск HTTP-сервера
internal/order/                       модели, интерфейс репозитория, ошибки
internal/handler/                     HTTP-маршруты, JSON, middleware, API-тесты
internal/service/                     бизнес-логика и модульные тесты
internal/repository/memory/           потокобезопасное in-memory-хранилище и тесты
docs/                                 спецификация OpenAPI и Swagger UI
```

Запрос обрабатывается по цепочке `handler → service → repository`. Зависимости передаются через конструкторы и интерфейсы. Хранилище синхронизировано для параллельных запросов и возвращает копии данных. Данные не сохраняются после завершения приложения

Каждый запрос логируется с помощью `log/slog` с полями `method`, `path`, `status`, `duration`, `request_id`. Panic recovery возвращает безопасный ответ `500`

## Проверка

```bash
go test ./...
go test ./internal/service/... -cover
go test ./... -race
go vet ./...
go build ./...
```

Тесты включают table-driven сценарии бизнес-логики, подмену репозитория через интерфейс, тесты in-memory-хранилища и HTTP API-тесты через `httptest`. Покрытие `internal/service` при проверке — **100%**

Список реализованных проверок: создание, валидация, расчёт стоимости, получение заказа и списка, успешные и запрещённые переходы, конечные статусы, история и её порядок, ошибки репозитория, HTTP-коды, неверный JSON, Swagger, логирование, recovery, конкурентные операции с хранилищем