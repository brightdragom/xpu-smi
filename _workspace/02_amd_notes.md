# AMD 어댑터 구현 노트

> 작성: amd-backend 에이전트, 2026-07-13
> 대상 파일: `internal/collector/amd/{amd.go, cli.go, sysfs.go}`
> 공개 API: `func New() collector.Collector` (유일한 export)

## 구현 개요

`Collector` 인터페이스를 3파일로 분리 구현했다 (패키지명 `amd`):

- `amd.go` — 어댑터 골격, `New()`, `Vendor()`, `Available()`, `Collect()`, `Close()`, 폴백 체인 탐지/메모이즈
- `cli.go` — `amd-smi` / `rocm-smi` JSON 파싱 + 공용 숫자/바이트 파싱 헬퍼
- `sysfs.go` — `/sys/class/drm/card*/device/` 직접 읽기 폴백

## 수집 경로 우선순위 (런타임 탐지)

1. `amd-smi metric --json` (ROCm 6+, 신형 — 우선)
2. `rocm-smi --showuse --showmemuse --showtemp --showpower --showmeminfo vram --showclocks --json` (구형, 널리 배포)
3. sysfs 직접 읽기 (도구 없이 amdgpu 커널 드라이버만 있어도 동작)

`detect()`가 우선순위대로 각 경로를 실행해 첫 성공(에러 없고 GPU ≥ 1개) 경로를 선택하고 struct에 메모이즈한다.
`Available()`은 3경로 모두 실패 시 패닉 없이 `false` 반환. `Collect()`는 메모이즈된 경로가 실패하면 전체 체인을 재탐지한다.

## 계약 준수 사항

- 외부 프로세스는 전부 `exec.CommandContext` + 2초 타임아웃(`cmdTimeout`). 각 CLI 소스가 `context.WithTimeout(ctx, cmdTimeout)`로 자체 바운드 → 호출자 취소도 존중.
- `exec.LookPath`로 도구 부재를 먼저 확인해 불필요한 exec 없이 빠르게 다음 경로로 폴백.
- 단위 변환:
  - sysfs `temp1_input` (milli-℃) → `/1000.0` → ℃
  - sysfs `power1_average` (µW) → `/1_000_000.0` → W
  - VRAM(byte)은 그대로. amd-smi의 `{value, unit}` 노드는 unit(MB/GB 등)을 보고 바이트로 환산(`toBytes`).
- **성공적으로 채운 필드만** `MarkSupported()`. CLI 값이 문자열이라 숫자 파싱 실패하면 해당 필드는 미표시(값 안 채움).
- 멀티 GPU에서 개별 카드 파싱 실패는 건너뛰고 나머지 계속 수집(sysfs: vendor 불일치/파일 부재 시 skip; CLI: 필드별 개별 처리).

## 경로별 지원 메트릭

| 필드 | amd-smi | rocm-smi | sysfs |
|------|:---:|:---:|:---:|
| UtilizationGPUPercent | O (`usage.gfx_activity`) | O (`GPU use (%)`) | O (`gpu_busy_percent`) |
| UtilizationMemoryPercent | O (`usage.umc_activity`) | O (`GPU Memory Allocated (VRAM%)` 등) | **X (미지원)** |
| MemoryUsedBytes | O (`mem_usage.used_vram`) | O (`VRAM Total Used Memory (B)`) | O (`mem_info_vram_used`) |
| MemoryTotalBytes | O (`mem_usage.total_vram`) | O (`VRAM Total Memory (B)`) | O (`mem_info_vram_total`) |
| TemperatureCelsius | O (`temperature.edge`) | O (`Temperature (Sensor edge) (C)`) | O (`hwmon*/temp1_input` /1000) |
| PowerWatts | O (`power.socket_power`) | O (`Average Graphics Package Power (W)`) | O (`hwmon*/power1_average` /1e6) |
| ClockGraphicsMHz | O (`clock.gfx_0.clk`) | O (`sclk clock speed:`) | O (`pp_dpm_sclk` 활성 `*` 레벨) |
| ClockMemoryMHz | O (`clock.mem_0.clk`) | O (`mclk clock speed:`) | O (`pp_dpm_mclk` 활성 `*` 레벨) |

### 미지원 필드 (의도적)

- **sysfs 경로의 `UtilizationMemoryPercent`**: `mem_info_vram_used/total`은 용량이지 메모리 대역폭 사용률이 아니므로 억지 계산하지 않고 `Supported`에 미표시(레퍼런스 amd.md 지침 준수).

## 파싱 견고성 설계

- 공용 `toFloat()`: JSON number / 숫자 문자열(단위 접미사 포함, 예 `"45.0 W"`, `"1500Mhz"`) / `{"value": ...}` 래퍼를 모두 처리. `numberRe` 정규식으로 선두 숫자 토큰 추출.
- rocm-smi 키명이 버전마다 흔들리므로 `findFloat/findBytes`는 후보 키 정확 매칭 → 실패 시 대소문자 무시 부분일치(substring)로 폴백.
- amd-smi 출력이 배열 형태 또는 gpu-키 오브젝트 형태 둘 다 올 수 있어 `decodeGPUList`가 정규화.
- power/temp 키는 세대별 대체 후보를 순서대로 시도(`socket_power`→`average_socket_power`→`current_socket_power`, `edge`→`hotspot`→`junction`).

## 검증 결과

- `go build ./internal/collector/amd/` — 통과 (exit 0). 전체 `go build ./...`는 intel 패키지의 미완성 심볼(`runIntelGPUTop`, `readEngineBusy`) 때문에 실패하나 이는 다른 벤더 소유. NVML의 cgo `deprecated` 경고는 무해.
- `go vet ./internal/collector/amd/` — 통과 (exit 0).
- 임시 스모크 테스트로 검증 후 삭제: `New().Available()==false`(이 머신에 AMD 없음, 정상), `Collect()`는 패닉 없이 sentinel 에러 반환. `trailingInt`/`toBytes`/`toFloat` 단위 테스트 통과.

## 미검증 항목 (하드웨어 부재)

- 이 개발 머신에 AMD GPU가 없어 **세 경로 모두 실제 데이터 파싱은 미검증**.
- 특히 `amd-smi metric --json`과 `rocm-smi --json`의 **실제 키 스키마는 설치 버전에 따라 다름** — 코드는 흔한 키 패턴 + 대체 후보 + 부분일치로 방어했으나, 실 하드웨어에서 `--json` 실제 출력으로 키 이름 최종 확인 필요(레퍼런스 amd.md의 지침).
- amd-smi VRAM `unit`이 `MB`일 때 1024^2로 환산(이진). 일부 빌드가 십진 MB를 쓸 가능성 있음 — 실측 시 확인 요망.
- sysfs `pp_dpm_sclk/mclk` 텍스트 포맷(`"1: 1500Mhz *"`)은 정규식(`sclkLineRe`)으로 활성 레벨 파싱 — 실 장비에서 포맷 확인 요망.

## 계약(types.go) 변경 필요 사항

- 없음. 기존 `Field*` 상수와 `GPUMetrics`로 모든 매핑 가능. 계약 파일은 수정하지 않았다.
