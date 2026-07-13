# xpu-smi

Nvidia · AMD · Intel GPU를 벤더 구분 없이 한 화면에서 보여주는 통합 모니터링 CLI/TUI 도구.

> 상태: 1차 개발 완료 (MVP, QA 통과). 실 GPU 하드웨어에서의 최종 검증은 진행 중 — [알려진 제한사항](#8-알려진-제한사항--미검증-항목) 참고.

## 목차

1. [개요](#1-개요)
2. [주요 특징](#2-주요-특징)
3. [설치 및 빌드](#3-설치-및-빌드)
4. [사용법](#4-사용법)
5. [출력 읽는 법](#5-출력-읽는-법)
6. [벤더별 동작 방식](#6-벤더별-동작-방식)
7. [문제 해결](#7-문제-해결)
8. [알려진 제한사항 / 미검증 항목](#8-알려진-제한사항--미검증-항목)
9. [아키텍처 개요](#9-아키텍처-개요)
10. [프로젝트 구조](#10-프로젝트-구조)

---

## 1. 개요

`nvidia-smi`, `rocm-smi`, `intel_gpu_top`은 각자 자기 벤더의 GPU만 보여준다. 한 머신에 여러 벤더의 GPU가 섞여 있거나, 벤더가 바뀔 때마다 다른 도구/다른 출력 포맷을 익혀야 하는 문제를 없애기 위해 xpu-smi를 만들었다.

xpu-smi는 각 벤더의 SDK/도구를 내부적으로 호출하되, 사용자에게는 **하나의 통일된 테이블/대시보드**만 보여준다. 어떤 벤더의 GPU가 꽂혀 있든 동일한 컬럼(사용률·메모리·온도·전력·클럭)으로 값을 확인할 수 있다.

**모니터링 범위:** 현재는 단일 노드(로컬 머신) 전용이다. 여러 서버의 GPU를 한 곳에서 모아 보는 기능은 아직 없다.

## 2. 주요 특징

- **벤더 무관 출력** — 렌더링 코드에 벤더별 분기가 전혀 없다. 새 벤더를 추가해도 화면 로직은 바뀌지 않는다.
- **두 가지 보기 모드** — 1회 스냅샷(스크립팅 친화적)과 실시간 TUI 대시보드.
- **안전한 벤더 미탑재 처리** — 이 머신에 Nvidia GPU가 없으면 nvidia 어댑터가 조용히 비활성화될 뿐, 프로그램이 죽지 않는다. AMD만 있는 서버, Intel만 있는 노트북 어디서든 동일하게 동작한다.
- **정직한 미지원 표시** — 값을 잴 수 없는 항목은 `0`이 아니라 `N/A`로 표시한다. "사용률 0%"와 "이 벤더/이 GPU에서는 측정 불가"를 혼동하지 않는다.
- **한 벤더의 장애가 전체를 막지 않음** — TUI에서 특정 벤더의 수집이 실패해도 그 벤더 행만 회색으로 표시(stale)되고, 나머지는 계속 갱신된다.

## 3. 설치 및 빌드

### 요구 사항

- Go 1.24 이상
- Linux (sysfs 기반 수집 경로가 Linux 전용)
- (선택) 벤더 도구가 설치되어 있으면 더 정확한 값을 얻는다 — 없어도 프로그램은 정상 동작한다:
  - Nvidia: 드라이버(`libnvidia-ml.so`)만 있으면 됨, 별도 CLI 불필요
  - AMD: `amd-smi` 또는 `rocm-smi` (없으면 sysfs로 자동 폴백)
  - Intel: `intel_gpu_top` (없으면 sysfs만으로 동작, 사용률 항목은 제한될 수 있음)

### 빌드

```bash
cd xpu-smi
go build -o xpu-smi ./cmd/xpu-smi
```

### 빌드 확인

```bash
go build ./...   # 전체 빌드
go vet ./...     # 정적 검사
```

> go-nvml 라이브러리가 cgo 컴파일 중 `-Wdeprecated-declarations` 경고를 다수 출력하는데, NVIDIA 헤더 내부 경고이며 실제 에러가 아니다. `go build`/`go vet`이 exit 0이면 정상이다.

## 4. 사용법

### 기본: 스냅샷 모드

```bash
./xpu-smi
```

한 번 수집해서 테이블을 출력하고 즉시 종료한다. 셸 스크립트나 cron, 모니터링 파이프라인에 넣기 좋다.

### 실시간 대시보드: TUI 모드

```bash
./xpu-smi --watch
```

터미널 전체 화면에 htop과 비슷한 방식으로 GPU 상태가 실시간 갱신된다. `q`, `esc`, `Ctrl+C` 중 아무 키로나 종료한다.

### 플래그

| 플래그 | 기본값 | 설명 |
|--------|--------|------|
| `--watch` | `false` | 실시간 TUI 모드로 실행 |
| `--interval` | `1s` | TUI 갱신 주기. 500ms 미만으로 지정해도 500ms로 강제 상향된다 (rocm-smi 등 서브프로세스 기반 수집이 너무 자주 fork되는 것을 방지) |

```bash
./xpu-smi --watch --interval 2s     # 2초마다 갱신되는 대시보드
```

### GPU가 하나도 없는 머신에서 실행하면

```bash
$ ./xpu-smi
감지된 GPU 없음 (nvidia/amd/intel 드라이버를 찾지 못했습니다)
```

에러가 아니라 정상 종료(exit code 0)다. 클라우드 인스턴스나 GPU 없는 개발 머신에서 실수로 실행해도 안전하다.

## 5. 출력 읽는 법

### 스냅샷 테이블 컬럼

```
VENDOR   IDX   NAME                 UTIL%   MEM             TEMP   POWER   CLOCK(G/M)
nvidia   0     NVIDIA RTX 4090      23%     4.1/24.0 GB     52C    120W    1800/10500 MHz
amd      0     AMD Instinct MI250   5%      2.0/64.0 GB     41C    85W    1200/1600 MHz
intel    0     Intel Arc A770 (discrete)   0%   N/A    38C   N/A   300 MHz
```

| 컬럼 | 의미 | 비고 |
|------|------|------|
| VENDOR | 벤더명 (`nvidia`/`amd`/`intel`) | 정렬 기준 1순위 |
| IDX | 해당 벤더 내 GPU 인덱스 | 정렬 기준 2순위. 동적 값(사용률 등)으로는 정렬하지 않아 스크립팅 시 행 위치가 매번 바뀌지 않는다 |
| NAME | GPU 모델명 | Intel은 통합/디스크리트 여부가 `(integrated)`/`(discrete)`로 표기됨 |
| UTIL% | GPU 사용률 | |
| MEM | 사용/전체 메모리 (GB) | 통합 GPU처럼 전용 VRAM이 없는 경우 `N/A` |
| TEMP | 온도 (℃) | |
| POWER | 소비 전력 (W) | |
| CLOCK(G/M) | 그래픽/메모리 클럭 (MHz) | 메모리 클럭이 없는 경우 그래픽 클럭만 표시 |

**`N/A`가 보이면:** 값이 0이라는 뜻이 아니라 "이 벤더/이 GPU 세대/이 실행 환경에서는 측정할 방법이 없다"는 뜻이다. 예를 들어 Intel 통합 GPU는 전용 VRAM이 없으므로 MEM 컬럼이 항상 `N/A`다.

### TUI 화면 구성

```
xpu-smi  —  3 GPU(s) detected (amd x1, intel x1, nvidia x1)

VENDOR   IDX   NAME                 UTIL%   MEM   TEMP   POWER   CLOCK(G/M)
...(스냅샷과 동일한 8컬럼 테이블, 매 interval마다 갱신)...

! amd stale: rocm-smi exit status 1        ← 특정 벤더 수집 실패 시에만 표시

q/esc/ctrl+c: quit   |   refresh: 1s
```

- 사용률 80% 이상, 온도 85℃ 이상인 값은 색상(주황/빨강)으로 강조되지만 **항상 숫자 텍스트와 함께** 표시된다 (색맹 사용자 배려, 색만으로 정보를 전달하지 않음).
- 특정 벤더의 수집이 실패하면 그 벤더의 행이 흐리게(dim) 표시되고 하단에 실패 사유가 나타난다. 마지막으로 성공했던 값을 유지한 채 "stale" 상태로 보여주며, 나머지 벤더는 정상적으로 계속 갱신된다.

## 6. 벤더별 동작 방식

### Nvidia

NVML(`github.com/NVIDIA/go-nvml`)을 통해 직접 드라이버와 통신한다. 별도 CLI 설치가 필요 없고, `libnvidia-ml.so`(드라이버 설치 시 함께 제공됨)만 있으면 동작한다. 드라이버가 없으면 조용히 비활성화된다.

### AMD

다음 순서로 사용 가능한 경로를 자동 탐지해 첫 성공 경로를 사용한다:

1. `amd-smi metric --json` (ROCm 6+ 최신 도구)
2. `rocm-smi --json` (구형, 널리 배포된 도구)
3. `/sys/class/drm/card*/device/` sysfs 직접 읽기 (도구 설치 없이 `amdgpu` 커널 드라이버만 있어도 동작)

sysfs 경로만 사용 가능한 경우 메모리 대역폭 사용률(`UtilizationMemoryPercent`)은 측정할 수 없어 `N/A`로 표시된다(용량 정보와 대역폭 사용률은 다른 값이라 억지로 계산하지 않음).

### Intel

`intel_gpu_top -J`(짧은 샘플링 스냅샷)와 sysfs(`i915`/`xe` 드라이버)를 조합한다. `intel_gpu_top`은 실행에 `root` 또는 `CAP_PERFMON` 권한이 필요할 수 있다 — 권한이 없으면 sysfs만으로 동작하며, 이 경우 얻을 수 있는 정보가 줄어든다.

**중요:** 통합 GPU(대부분의 노트북/데스크톱 내장 그래픽)는 전용 VRAM이 없어 시스템 RAM을 공유한다. xpu-smi는 이를 시스템 RAM 수치로 대충 채우지 않고 메모리 관련 컬럼을 정직하게 `N/A`로 표시한다. 이름 표기에 `(integrated)`/`(discrete)`가 자동으로 붙어 어떤 종류인지 구분할 수 있다.

## 7. 문제 해결

| 증상 | 원인 | 해결 |
|------|------|------|
| "감지된 GPU 없음"이 나오는데 GPU가 있다 | 드라이버 미설치, 권한 부족, 또는 sysfs 접근 차단 | Nvidia: `nvidia-smi`가 정상 동작하는지 먼저 확인. AMD/Intel: `ls /sys/class/drm/`로 카드가 보이는지 확인, 필요 시 `sudo`로 재실행 |
| AMD 어댑터가 `N/A` 위주로 나온다 | `amd-smi`/`rocm-smi` 없이 sysfs만 사용 중 | `rocm-smi` 또는 `amd-smi`를 설치하면 더 많은 필드(특히 메모리 대역폭 사용률)를 얻을 수 있다 |
| Intel MEM 컬럼이 항상 `N/A` | 통합 GPU라 VRAM 자체가 없음 | 정상 동작이다 — 디스크리트(Arc) GPU에서는 값이 채워진다 |
| TUI에서 특정 벤더에 `! ... stale` 경고가 뜬다 | 해당 벤더의 마지막 수집 시도가 실패 | 하단 메시지의 에러 내용을 확인. 도구가 일시적으로 바쁘거나(rocm-smi 등) 권한 문제일 수 있으며, 다음 갱신 주기에 자동 재시도된다 |
| `--interval 100ms`을 줬는데 1초처럼 느리다 | 하한선(500ms) 강제 적용 | 의도된 동작 — 서브프로세스 기반 수집(rocm-smi 등)이 과도하게 자주 실행되는 것을 방지하기 위함 |

## 8. 알려진 제한사항 / 미검증 항목

이 프로젝트는 실 GPU 하드웨어가 없는 환경에서 개발되었다(정적 코드 검증 + QA 교차 검증으로 대체, `_workspace/03_qa_report.md` 참조). 다음 항목은 실제 하드웨어에서의 추가 확인이 필요하다:

- `amd-smi`/`rocm-smi`의 실제 `--json` 출력 키 스키마는 설치 버전마다 다를 수 있음 (코드는 후보 키 + 부분일치 폴백으로 방어했으나 최종 확인 필요)
- `intel_gpu_top -J`의 실제 출력 스키마와 멀티 GPU 환경에서의 디바이스 선택자(`-d`) 동작
- 멀티 GPU 환경에서 카드별 격리 수집이 실제로 올바르게 동작하는지
- TUI의 실제 렌더링 결과(색상 임계값 트리거, 터미널 폭 대응 등)

기능적 제약(의도된 설계):

- 다중 노드/클러스터 수집 미지원 (단일 노드 전용)
- Intel의 엔진별(Render/Video/Blitter) 세분화된 사용률은 노출하지 않음 — `Render/3D` 엔진의 대표값만 사용
- Intel Level Zero(Sysman) API 미구현 — MVP는 `intel_gpu_top`/sysfs 조합만 사용 (`_workspace/02_intel_notes.md`에 향후 확장 방향 기록)
- `--json` 출력, 벤더/인덱스 필터, 프로세스별 GPU 사용량 등은 아직 없음 (선택 요구사항, `docs/DEVELOPMENT_PLAN.md` 참고)

## 9. 아키텍처 개요

```
[벤더 어댑터 3종] → Collector 인터페이스 → [레지스트리 Detect()] → [렌더러]
 nvidia/amd/intel     (공통 계약)           (사용 가능한 것만 활성화)   snapshot/TUI
```

모든 어댑터는 동일한 `Collector` 인터페이스(`internal/collector/types.go`)를 구현한다. 핵심 설계 원칙 두 가지:

1. **`Available()`은 탐지이지 강제 초기화가 아니다** — 이 머신에 해당 벤더 GPU가 없으면 패닉 없이 `false`를 반환해 스스로 비활성화한다.
2. **미지원 필드는 0이 아니라 `Supported` 플래그로 표현한다** — 렌더러는 이 플래그를 보고 `N/A`를 표시할지 실제 값을 표시할지 결정하며, 벤더별 분기 없이 항상 동일한 로직으로 동작한다.

상세 설계 배경과 요구사항은 [`docs/DEVELOPMENT_PLAN.md`](docs/DEVELOPMENT_PLAN.md)를 참고한다.

## 10. 프로젝트 구조

```
xpu-smi/
├── go.mod
├── cmd/xpu-smi/main.go            # 플래그 파싱, 어댑터 등록, 모드 분기
├── internal/
│   ├── collector/
│   │   ├── types.go               # Collector 인터페이스, GPUMetrics, Supported 플래그
│   │   ├── registry.go            # Register / Detect
│   │   ├── nvidia/                # NVML 어댑터
│   │   ├── amd/                   # amd-smi → rocm-smi → sysfs 어댑터
│   │   └── intel/                 # intel_gpu_top + sysfs 어댑터
│   └── render/
│       ├── snapshot.go            # 1회 스냅샷 테이블
│       └── tui.go                 # --watch 실시간 대시보드
├── docs/DEVELOPMENT_PLAN.md       # 개발 계획 및 실행 이력
└── _workspace/                    # 설계 문서·벤더별 구현 노트·QA 리포트 (감사 추적용)
```
