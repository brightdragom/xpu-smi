# xpu-smi 아키텍처 설계 (계약 문서)

> 작성: 2026-07-13, 리더(아키텍트 역할). 모든 구현 에이전트는 이 계약을 기준으로 작업한다.

## 확정된 계약 파일 (수정 금지)

- `internal/collector/types.go` — `Collector` 인터페이스, `GPUMetrics`, `Field*` 상수, `NewMetrics`/`MarkSupported`/`IsSupported` 헬퍼
- `internal/collector/registry.go` — `Register(factory)` / `Detect()`
- 계약 변경이 필요하면 직접 수정하지 말고 노트 파일에 사유를 기록하고 리더에게 보고

## 단위 규칙 (절대 준수)

| 필드 | 단위 | 주의 |
|------|------|------|
| MemoryUsedBytes / MemoryTotalBytes | byte | NVML은 byte 그대로, sysfs도 byte 그대로 |
| TemperatureCelsius | ℃ | sysfs `temp1_input`은 milli-celsius → /1000 |
| PowerWatts | W | NVML은 mW → /1000, sysfs `power1_average`는 µW → /1e6 |
| ClockGraphicsMHz / ClockMemoryMHz | MHz | 그대로 |
| Utilization*Percent | 0~100 | 그대로 |

## Supported 플래그 규칙

`NewMetrics()`는 빈 Supported 맵으로 시작한다(기본값 = 미지원, fail-safe). 어댑터는 **성공적으로 채운 필드만** `MarkSupported(collector.Field...)`로 표시한다. 값을 채웠는데 MarkSupported를 빠뜨리면 N/A로 표시된다 — 반대(쓰레기 값이 진짜처럼 표시)보다 안전한 방향이다.

## 파일 소유권 (병렬 충돌 방지)

| 파일/디렉토리 | 소유자 | 비고 |
|--------------|--------|------|
| `internal/collector/nvidia/` | nvidia 에이전트 | 패키지명 `nvidia` |
| `internal/collector/amd/` | amd 에이전트 | 패키지명 `amd` |
| `internal/collector/intel/` | intel 에이전트 | 패키지명 `intel` |
| `internal/render/` | frontend 에이전트 | 패키지명 `render` |
| `cmd/xpu-smi/main.go` | **리더만** | 모든 에이전트 완료 후 리더가 배선 |
| `go.mod` / `go.sum` | **리더만** | 의존성은 이미 받아둠. `go get`/`go mod tidy` 실행 금지 |
| `_workspace/02_{자기이름}_notes.md` | 각 에이전트 | 구현 노트/미지원 필드/제약 기록 |

## 벤더 어댑터 공개 API 규약

각 벤더 패키지는 다음 하나만 공개한다:

```go
// package nvidia | amd | intel
func New() collector.Collector
```

리더가 main.go에서 `collector.Register(nvidia.New)` 형태로 배선한다.

## 렌더러 공개 API 규약 (frontend)

```go
package render

// Snapshot은 이미 수집된 메트릭을 테이블로 1회 출력한다.
func Snapshot(w io.Writer, metrics []collector.GPUMetrics) error

// RunTUI는 interval마다 collectors에서 재수집하며 화면을 갱신한다.
// 한 벤더의 Collect 실패는 해당 행만 N/A/stale 처리하고 계속 진행한다.
func RunTUI(ctx context.Context, collectors []collector.Collector, interval time.Duration) error
```

## 사용 가능한 외부 의존성 (이미 go.mod에 있음)

- `github.com/NVIDIA/go-nvml` v0.13.3-1 (nvidia 전용)
- `github.com/charmbracelet/bubbletea` v1.3.10, `lipgloss` v1.1.0 (frontend 전용)
- 이 외의 의존성 추가 금지 (필요하면 노트에 기록하고 리더 승인)

## 검증 기준

- 자기 패키지가 `go build ./internal/...` 통과
- 이 개발 머신에는 실 GPU가 없다 — `Available()`이 false를 반환하며 패닉하지 않는 것이 정상 동작
- 하드웨어 의존 검증 항목은 노트에 "미검증"으로 명시
