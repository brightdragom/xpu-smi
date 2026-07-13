# Nvidia 어댑터 구현 노트

> 작성: nvidia-backend 에이전트 / 2026-07-13
> 대상 파일: `internal/collector/nvidia/nvidia.go`
> 라이브러리: `github.com/NVIDIA/go-nvml` v0.13.3-1 (`pkg/nvml`)

## 구현 요약

- 공개 API는 `func New() collector.Collector` 하나만 (`nvidiaCollector` 는 unexported).
- 어댑터는 상태를 갖지 않음(stateless). 매 `Collect` 호출이 자체 `nvml.Init()` / `defer nvml.Shutdown()` 로 감싼다. NVML은 Init/Shutdown을 내부적으로 refcount 하므로 중복 호출 안전.
- `Close()` 는 no-op (장기 보유 핸들/세션 없음).

## 단위 변환 (계약 준수 확인)

| 필드 | NVML 호출 | NVML 단위 | 변환 | 결과 단위 |
|------|-----------|-----------|------|-----------|
| PowerWatts | `GetPowerUsage()` | milliwatt (uint32) | `/1000.0` | Watt |
| MemoryUsedBytes / MemoryTotalBytes | `GetMemoryInfo()` | byte (uint64) | 그대로 | byte |
| TemperatureCelsius | `GetTemperature(TEMPERATURE_GPU)` | ℃ (uint32) | 그대로 | ℃ |
| ClockGraphicsMHz | `GetClockInfo(CLOCK_GRAPHICS)` | MHz (uint32) | 그대로 | MHz |
| ClockMemoryMHz | `GetClockInfo(CLOCK_MEM)` | MHz (uint32) | 그대로 | MHz |
| Utilization{GPU,Memory}Percent | `GetUtilizationRates()` | 0~100 % (uint32) | 그대로 | % |

- **전력만 변환 필요** (mW → W). 나머지는 NVML 단위가 계약 단위와 일치.

## Supported 플래그 정책

- `NewMetrics()` 의 빈 맵에서 시작 (fail-safe = 미지원).
- 각 NVML 호출이 `nvml.SUCCESS` 를 반환했을 때만 필드를 채우고 `MarkSupported(...)` 호출.
- `NOT_SUPPORTED` 등 실패 반환 시 해당 필드는 채우지 않고 Supported 미표시 → 렌더러에서 N/A.
- `Name`, `UUID` 는 식별 필드라 Supported 대상이 아님(계약의 Field* 상수에도 없음). best-effort로만 채운다.
- 유틸/메모리는 두 필드를 한 호출(`GetUtilizationRates`, `GetMemoryInfo`)로 얻으므로 성공 시 두 필드를 함께 MarkSupported.

## 에러/폴백 정책

- `nvml.Init()` 실패(드라이버/libnvidia-ml.so 없음, 권한 없음, 디바이스 없음) → `Available()` 는 `false`, `Collect()` 는 `(nil, nil)` 반환. 패닉/`os.Exit` 없음.
- `DeviceGetCount` 실패 → `(nil, nil)`.
- 개별 GPU `DeviceGetHandleByIndex` 실패 → 그 인덱스만 `continue`, 나머지 계속 수집 (멀티 GPU 격리).
- 개별 메트릭 호출 실패 → 해당 필드만 건너뜀 (GPU 자체는 계속 채움).
- `Collect` 는 GPU 루프 진입 시 `ctx.Err()` 를 검사해 컨텍스트 취소를 존중. NVML 호출은 로컬 라이브러리 호출이라 개별 호출 단위 타임아웃은 불필요(서브프로세스 아님).

## 미지원 / 조건부 지원 필드

- 구조적으로 항상 미지원인 필드는 **없음**. 8개 Field* 상수 모두 최신 데이터센터/컨슈머 GPU에서 지원.
- 단, 다음은 하드웨어/환경에 따라 `NOT_SUPPORTED` 가능 → 런타임에 자동으로 Supported 미표시 처리됨:
  - `UtilizationMemoryPercent`: 구형 GPU / vGPU 에서 미지원 가능.
  - `ClockMemoryMHz`: 일부 통합/모바일 세대에서 미지원 가능.
  - `PowerWatts`: 전력 센서 없는 저가/구형 카드에서 미지원 가능.
  - `TemperatureCelsius`: vGPU 게스트 등에서 미지원 가능.

## NVML API 제약 / 버전 확인 사항 (go-nvml v0.13.3-1)

- 사용한 함수 시그니처(생성 코드 `zz_generated.api.go` 기준 확인):
  - `Init() Return`, `Shutdown() Return`, `DeviceGetCount() (int, Return)`, `DeviceGetHandleByIndex(int) (Device, Return)`
  - `Device.GetName() (string, Return)`, `GetUUID() (string, Return)`
  - `GetUtilizationRates() (Utilization{Gpu,Memory uint32}, Return)`
  - `GetMemoryInfo() (Memory{Total,Free,Used uint64}, Return)`
  - `GetTemperature(TemperatureSensors) (uint32, Return)`
  - `GetPowerUsage() (uint32, Return)`
  - `GetClockInfo(ClockType) (uint32, Return)`
- 상수: `nvml.SUCCESS`, `nvml.TEMPERATURE_GPU`, `nvml.CLOCK_GRAPHICS`, `nvml.CLOCK_MEM` 사용.
- `GetMemoryInfo_v2` 도 존재하나(신형), 계약이 Used/Total만 요구하므로 호환성 넓은 `GetMemoryInfo` 사용.
- cgo 빌드 시 go-nvml 라이브러리 자체에서 다수의 `-Wdeprecated-declarations` 경고 발생. **우리 코드와 무관**하며 빌드/vet 결과는 정상(exit 0). 우리가 호출하는 함수 중 deprecated 없음.

## 검증 결과

- `go build ./...` → exit 0 (라이브러리 cgo deprecation 경고만 출력, 에러 아님).
- `go vet ./internal/collector/nvidia/` → exit 0.
- 임시 스모크 테스트(작성 후 삭제)로 런타임 확인: 이 GPU 미탑재 머신에서
  `Available() = false` (패닉 없음), `Collect() -> 0 metrics, err=nil`, `Close() -> nil`.

## 미검증 항목 (하드웨어 의존 — 실 GPU 없어 확인 불가)

- 실제 NVIDIA GPU에서의 값 정확성/단위(특히 mW→W 변환의 실측 일치).
- 멀티 GPU 환경에서 인덱스별 분리 수집 및 1장 실패 격리 동작.
- vGPU/구형 세대에서 특정 필드의 `NOT_SUPPORTED` 실제 반환 및 Supported 미표시 동작.
- MIG(Multi-Instance GPU) 환경은 고려하지 않음(계약 범위 밖). 필요 시 별도 확장 논의.

## 계약 관련 이슈

- 없음. `internal/collector/types.go` 의 `Collector` / `GPUMetrics` / `Field*` 상수를 그대로 구현. 계약 변경 요청 사항 없음.
