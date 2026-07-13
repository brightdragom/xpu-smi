# 통합 정합성 검증 리포트

> 작성: qa-integrator 에이전트 / 2026-07-13
> 대상: `internal/collector/{types.go, registry.go, nvidia/, amd/, intel/}`, `internal/render/{snapshot.go, tui.go}`, `cmd/xpu-smi/main.go`
> 방법: 계약(types.go)과 구현을 동시에 열고 교차 비교 + `go build`/`go vet`/`gofmt`/실행 스모크

## 요약 (집계)

- **PASS: 21** — 인터페이스 준수 12, 렌더러 소비 6, 레지스트리 1, 안전성 2
- **FAIL: 0** (기능/계약 결함 없음)
- **경미 지적(비-FAIL): 1** — `amd.go` gofmt 미정렬(주석 들여쓰기, 동작 무관)
- **관찰(비-결함): 1** — `UtilizationMemoryPercent`는 수집되나 렌더러가 표시하지 않음(설계상 UTIL% 컬럼은 GPU util만)
- **미검증: 6종** (실 GPU 하드웨어 필요)

---

## 빌드 상태

- `go build ./...` : **PASS** (exit 0). 출력의 `-Wdeprecated-declarations` 경고는 전부 `go-nvml` 라이브러리 내부 cgo 경고이며 우리 코드와 무관, 빌드 에러 아님.
- `go vet ./...` : **PASS** (exit 0).
- `gofmt -l internal/ cmd/` : `internal/collector/amd/amd.go` 1건 리스트됨(아래 경미 지적).
- 실행 스모크(이 GPU 미탑재 머신): `xpu-smi` 실행 → `감지된 GPU 없음 (...)` 출력, **exit 0**, 패닉 없음.

---

## 인터페이스 준수

| 어댑터 | 항목 | 결과 | 근거(파일:라인) |
|--------|------|------|-----------------|
| nvidia | 전력 mW→W 변환 | PASS | nvidia/nvidia.go:109 `float64(powerMW) / 1000.0` |
| nvidia | 온도 ℃ 그대로 | PASS | nvidia/nvidia.go:103 (NVML은 ℃ 반환) |
| nvidia | 메모리 byte 그대로 | PASS | nvidia/nvidia.go:93-94 |
| nvidia | 클럭 MHz 그대로 / util 0~100 | PASS | nvidia/nvidia.go:83-84,115,119 |
| nvidia | 성공 시에만 MarkSupported | PASS | nvidia/nvidia.go:85-88,95-98,104,110,116,120 (각 `ret==SUCCESS` 가드 후 마크) |
| amd | sysfs 온도 milli-℃→/1000 | PASS | amd/sysfs.go:98 `float64(v) / 1000.0` |
| amd | sysfs 전력 µW→/1e6 | PASS | amd/sysfs.go:103 `float64(v) / 1_000_000.0` |
| amd | VRAM byte(단위 노드 환산) | PASS | amd/cli.go:258-274 `toBytes`, amd/sysfs.go:85-91 |
| amd | sysfs 경로 UtilizationMemoryPercent 미표시(의도적) | PASS | amd/sysfs.go:118-119 (주석 명시, MarkSupported 호출 없음) |
| amd | CLI 전력/온도 단위(amd-smi W, rocm-smi W, ℃) 그대로 | PASS | amd/cli.go:78-81,85-88,183-188 |
| intel | sysfs 전력 µW→/1e6, 온도 milli-℃→/1000 | PASS | intel/sysfs.go:194-199,206-208 |
| intel | 통합 GPU 메모리 미표시(시스템 RAM 오표기 방지) | PASS | intel/sysfs.go:131-139 (VRAM 파일 존재 시에만 MarkSupported), intel/intel.go:6-9 |

세부 근거:
- **단위 변환 전수 확인 결과 누락/오배수 없음.** 계약(types.go:26-28, 설계 문서 단위표)이 요구한 3개 변환 지점(NVML mW→/1000, sysfs milli-℃→/1000, sysfs µW→/1e6)이 모두 정확한 제수로 구현됨. 세 어댑터를 나란히 비교해도 불일치 없음(아래 단위 일관성 표).
- **Supported fail-safe 준수.** 모든 어댑터가 `collector.NewMetrics()`(빈 맵)로 시작하고, 값 채움 성공 직후에만 `MarkSupported(collector.Field...)` 호출. "값 채움 + 마크 누락" 또는 "마크 + 값 미채움" 불일치 없음.
- **미지원 필드 의도적 처리.** amd sysfs의 `UtilizationMemoryPercent`(sysfs.go:118-119)와 intel 통합 GPU의 메모리/메모리util(sysfs.go:131-139)은 값을 억지로 0으로 채우지 않고 미표시 → 렌더러가 N/A. 노트(02_amd/02_intel)의 선언과 코드 일치.
- **인터페이스 시그니처.** 3개 어댑터 모두 `Vendor()/Available()/Collect(ctx)/Close()` 구현 및 `New() collector.Collector` 단일 export. 구조적 타이핑이 아닌 값 의미까지 교차 확인 완료. 빌드 통과로 시그니처 일치도 확인.

---

## 렌더러 소비 정합성

| 항목 | 결과 | 근거(파일:라인) |
|------|------|-----------------|
| formatUtil — Supported 확인 후 출력 | PASS | render/snapshot.go:46-51 (`IsSupported(FieldUtilizationGPUPercent)` 후 값) |
| formatMem — used·total 둘 다 Supported일 때만 | PASS | render/snapshot.go:55-63 |
| formatTemp / formatPower — Supported 확인 | PASS | render/snapshot.go:65-77 |
| formatClock — graphics 미지원 시 전체 N/A, graphics만 지원 시 단일 표시 | PASS | render/snapshot.go:83-94 |
| 벤더별 분기(`if vendor==...`) 부재 | PASS | render/snapshot.go:5-8, render/tui.go:24,72 (주석대로 Vendor는 표시 라벨로만 사용, 값 렌더링은 전부 Supported 맵 기반) |
| TUI 임계값 색상도 Supported 확인 후 적용 | PASS | render/tui.go:315-322 (`IsSupported` 확인 + stale 아닐 때만) |

세부:
- **Supported 미확인 출력 지점 없음.** 8개 컬럼 중 메트릭 5종(UTIL%/MEM/TEMP/POWER/CLOCK)은 모두 `format*` 헬퍼가 `IsSupported`로 게이팅. `VENDOR`/`IDX`/`NAME`은 식별 필드(NewMetrics가 항상 설정, Name은 빈 문자열 시 N/A: snapshot.go:97-102)라 Supported 대상 아님 — 계약과 일치.
- **뷰 일관성.** TUI(tui.go:325-334)가 스냅샷의 `format*` 헬퍼를 그대로 재사용 → 두 뷰가 구조적으로 어긋날 수 없음.
- **필드명 오타/철자 불일치 없음.** 렌더러가 참조하는 `Field*` 상수·구조체 필드가 types.go 정의와 일치(빌드 통과가 1차 보증, 코드 대조로 2차 확인).

관찰(비-결함): `UtilizationMemoryPercent`는 nvidia(nvidia.go:84)·amd·intel이 수집/마크하지만 렌더러에 대응 컬럼이 없어 표시되지 않는다. 테이블 UTIL% 컬럼은 GPU util 전용이라는 프론트엔드 설계(02_frontend_notes)와 일치하며 계약 위반 아님. 향후 메모리 util 컬럼 추가 시 이미 데이터는 준비되어 있음.

---

## 레지스트리 등록

| 항목 | 결과 | 근거 |
|------|------|------|
| nvidia/amd/intel 3개 어댑터 모두 Register | PASS | cmd/xpu-smi/main.go:52-54 `collector.Register(nvidia.New)`, `amd.New`, `intel.New` |

- `registry.go`의 `Detect()`(registry.go:18-29)가 `Available()==true`인 어댑터만 유지하고 나머지는 `Close()` 후 드롭 — 조용한 누락 없음. 3벤더 전부 배선되어 하드웨어 존재 시 감지 보장.

---

## 벤더 미탑재 환경 안전성

- **`Available()==false` 경로: PASS.** 세 어댑터 모두 초기화 실패를 error가 아닌 `bool false`로 처리, panic/os.Exit 없음.
  - nvidia: nvidia.go:36-42 — `nvml.Init()` 비-SUCCESS 시 false.
  - amd: amd.go:75-81 — `detect()`가 3경로 모두 실패 시 nil→false, 패닉 없음.
  - intel: intel.go:61-86 — Intel 카드 부재 또는 소스 불가 시 false.
- **panic/os.Exit 코드 스캔: PASS.** `internal/`·`cmd/` 전수 grep 결과 실제 `os.Exit`는 `cmd/xpu-smi/main.go:45` 한 곳뿐이며, 이는 `run()` 반환 에러에 대한 최상위 종료 처리(Available/Collect 경로 아님). 어댑터 코드에 `panic`/`os.Exit`/`log.Fatal` 없음. tui.go의 "panic" 언급은 오히려 `recover`로 방어하는 코드(tui.go:110-115).
- **실행 스모크: PASS.** 바이너리 실행 시 `감지된 GPU 없음` 출력 후 exit 0, 크래시 없음.
- **TUI 복원력: PASS(코드 경로).** `collectOne`(tui.go:108-127)이 각 collector를 `recover`로 감싸 panic을 에러로 변환, 한 벤더 실패가 대시보드 전체를 죽이지 않음. 실패 벤더는 마지막 성공값 유지+stale 표시(tui.go:146-155,233-252).

---

## 단위 일관성 표

| 필드 | nvidia 단위/변환 | amd 단위/변환 | intel 단위/변환 | 표준과 일치? |
|------|-----------------|--------------|-----------------|-------------|
| PowerWatts | mW → /1000 (nvidia.go:109) | sysfs µW → /1e6 (sysfs.go:103); CLI는 W 그대로 (cli.go:78,183) | sysfs µW → /1e6 (sysfs.go:194-199) | **일치** |
| TemperatureCelsius | ℃ 그대로 (nvidia.go:103) | sysfs milli-℃ → /1000 (sysfs.go:98); CLI는 ℃ 그대로 (cli.go:85,179) | sysfs milli-℃ → /1000 (sysfs.go:206) | **일치** |
| MemoryUsedBytes / TotalBytes | byte 그대로 (nvidia.go:93-94) | byte 그대로; amd-smi는 unit 노드로 환산 (sysfs.go:85-91, cli.go:258-274) | byte 그대로, **디스크리트만** Supported (sysfs.go:214-230) | **일치** |
| UtilizationMemoryPercent | GetUtilizationRates.Memory 그대로 (nvidia.go:84) | CLI만 지원, **sysfs 미지원** (sysfs.go:118-119) | 통합 미지원, 디스크리트만 used/total 파생 (sysfs.go:135-138) | **일치**(미지원=N/A) |
| ClockGraphicsMHz / MemoryMHz | MHz 그대로 (nvidia.go:115,119) | MHz 그대로 (sysfs.go:109-116, cli.go:102-110) | Graphics MHz 그대로; **Memory 클럭 전 세대 미지원** (sysfs.go:141) | **일치**(intel mem클럭=N/A) |
| Utilization GPU % | 0~100 그대로 (nvidia.go:83) | 0~100 그대로 (sysfs.go:79-81, cli.go:66-68) | intel_gpu_top Render/3D busy 0~100 (gputop.go:121-127) | **일치** |

세 어댑터를 나란히 비교해도 스케일/제수 불일치 없음. 벤더별 "미지원" 표시는 계약의 Supported fail-safe로 일관되게 처리됨.

---

## 경미 지적 (비-FAIL, 리더 판단)

1. **gofmt 미정렬 — `internal/collector/amd/amd.go` (패키지 doc 주석 3~13행 부근).**
   - 현상: doc 주석의 번호 목록 들여쓰기가 공백이라 `gofmt`가 탭 정렬로 재포맷 대상(`gofmt -l`에 리스트됨). 동작/컴파일에는 무영향.
   - 수정 방법: `gofmt -w internal/collector/amd/amd.go` 실행(주석 리스트 들여쓰기를 탭으로 정규화). 리더가 최종 커밋 전 일괄 `gofmt -w` 권장.

---

## 미검증 항목 (실 GPU 하드웨어 필요 — 이 환경에서 확인 불가, 실패 아님)

- **nvidia**: 실 NVIDIA GPU에서 값 정확성/mW→W 실측 일치, 멀티 GPU 인덱스 분리 및 1장 실패 격리, vGPU/구형 세대의 `NOT_SUPPORTED` 실제 반환 시 Supported 미표시 동작.
- **amd**: `amd-smi metric --json` / `rocm-smi --json`의 **실제 키 스키마**(설치 버전별 상이) 정합, amd-smi VRAM `unit`이 MB일 때 1024²(이진) 환산의 실측 타당성, sysfs `pp_dpm_sclk/mclk` 텍스트 포맷(`"1: 1500Mhz *"`) 파싱.
- **intel**: `intel_gpu_top -J` 실제 출력 스키마(엔진 키/ busy 필드)와 스트리밍 truncation 처리, `-d drm:/dev/dri/cardN` 선택자 문법(버전별), xe 주파수 sysfs 경로 존재 여부, 멀티 Intel GPU 시 util 근사치 문제.
- **frontend**: 실제 TTY에서 TUI 렌더/색상/altscreen, stale 전환 실측, UTIL≥80/TEMP≥85 색상 임계값 트리거.
- **공통**: 3벤더 동시 탑재 환경에서의 통합 스냅샷/정렬 실측.
- 사유: 이 개발 머신에는 실 GPU(nvidia/amd/intel)가 없고(현재 vendor 0x1b36 QEMU 가상 GPU만 존재), TTY도 없어 위 항목은 정적 코드 검증으로만 확인함. 계약/단위/안전성은 코드 대조로 PASS 판정.
