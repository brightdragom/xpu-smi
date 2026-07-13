# Intel 어댑터 구현 노트

> 작성: intel-backend 에이전트. 대상: `internal/collector/intel/` (패키지 `intel`).
> 공개 API: `func New() collector.Collector` 하나. 내부는 3파일로 분리:
> `intel.go`(Collector/Available/Collect/Close), `sysfs.go`(카드 탐지·sysfs 읽기),
> `gputop.go`(intel_gpu_top 실행·JSON 파싱).

## 데이터 경로 (MVP)

1. **sysfs** `/sys/class/drm/card{N}/` — 클럭, 전력, 온도, (디스크리트 한정) VRAM.
   권한 없이도 대부분 읽힘. i915와 xe 드라이버 파일 레이아웃을 모두 시도.
2. **intel_gpu_top -J -s 200** — 엔진별 busy(%)를 JSON 스냅샷으로. `Render/3D` 엔진의
   busy 값을 `UtilizationGPUPercent`로 사용. root 또는 CAP_PERFMON 필요할 수 있음.
   스트리밍 도구라 `exec.CommandContext`로 3초 타임아웃을 걸고 kill; 종료 에러는
   무시하고 stdout에서 파싱된 마지막 완전 샘플로 성공 여부를 판단.

## 통합 GPU vs 디스크리트 GPU(Arc) — 지원 메트릭 차이 (핵심)

| 필드 | 통합 GPU | 디스크리트(Arc) | 근거 |
|------|:---:|:---:|------|
| UtilizationGPUPercent | O | O | intel_gpu_top `Render/3D` busy |
| ClockGraphicsMHz | O | O | sysfs `gt_cur_freq_mhz`(i915) 또는 `gt*/freq*/act_freq`(xe) |
| PowerWatts | 세대별 | O | sysfs `hwmon/*/power1_average` (µW→W). 구형 통합은 미노출 가능 |
| TemperatureCelsius | 세대별 | O | sysfs `hwmon/*/temp1_input` (m℃→℃) |
| **MemoryUsedBytes / MemoryTotalBytes** | **X (N/A)** | O | `mem_info_vram_*` 는 디스크리트만 존재 |
| UtilizationMemoryPercent | X (N/A) | O | VRAM used/total 로 파생 (total>0일 때만) |
| ClockMemoryMHz | X (N/A) | X (N/A) | i915/xe 공통 신뢰 소스 없음 |

### 통합 GPU의 메모리 처리 — 이 어댑터의 가장 중요한 설계 포인트
통합 GPU는 전용 VRAM이 없고 시스템 RAM을 공유한다. **시스템 RAM을 VRAM인 척
채우지 않는다.** `mem_info_vram_used/total` sysfs 파일이 없으면 메모리 관련
필드를 `MarkSupported` 하지 않아 렌더러가 N/A로 표시한다. Nvidia/AMD 어댑터를
그대로 흉내 내면 안 되는 지점.

`displayName()`은 `mem_info_vram_*` 존재 여부로 "(discrete)"/"(integrated)"를
자동 판별해 이름에 표기한다(예: `Intel GPU [xe] (integrated)`). Name은 식별
메타데이터라 Supported 게이팅 대상 아님.

## Available() 의미 (하드웨어 부재 vs 권한 부족 구분)

- Intel DRM 카드 없음(vendor id != 0x8086) → **false (하드웨어 부재)**. 대부분의
  머신에서 정상.
- Intel 카드 있고 sysfs 메트릭 1개 이상 읽힘 → **true**.
- Intel 카드 있고 sysfs는 비었으나 intel_gpu_top 실행 성공 → **true**.
- Intel 카드 있으나 어떤 소스도 읽히지 않음(대개 권한 문제: intel_gpu_top의
  root/CAP_PERFMON 미충족 + sysfs 접근 차단) → **false (권한 부족)**.

부재와 권한 부족은 둘 다 false지만 다른 상황이다. 패닉/os.Exit 없이 항상 bool 반환.
벤더 판별은 `/sys/class/drm/cardN/device/vendor == 0x8086` 으로 하고, 커넥터 항목
(`cardN-DP-1` 등)은 정규식 `^card[0-9]+$` 로 제외한다.

## 단위 변환

- `power1_average`: microwatt → `/1_000_000.0` (W)
- `temp1_input`: milli-celsius → `/1000.0` (℃)
- `gt_cur_freq_mhz` / xe `act_freq`: MHz 그대로 (uint32)
- `mem_info_vram_*`: byte 그대로 (uint64)

## 미검증 항목 (이 개발 머신에 Intel GPU 없음)

이 머신은 vendor 0x1b36(QEMU 가상 GPU)만 있고 intel_gpu_top 미설치.
`Available()==false`, `Collect()`가 빈 슬라이스 반환하며 패닉 없음은 확인.
다음은 실 하드웨어에서 재검증 필요:

- intel_gpu_top `-J` 실제 출력 스키마(엔진 키 이름, busy 필드 형태)와 스트리밍
  종료 시 truncation 처리. 파서는 truncated 배열/bare-object 스트림 모두 처리하도록
  작성하고 truncation 케이스는 단위 테스트로 검증했으나, 실제 도구 출력과의 정합은 미검증.
- intel_gpu_top 디바이스 선택자 `-d drm:/dev/dri/cardN` 문법(버전별 상이 가능).
  실패 시 선택자 없이 재시도하는 폴백을 두었다 — **멀티 Intel GPU 환경에서는
  선택자가 안 먹으면 기본 GPU 부하를 모든 카드에 귀속시킬 수 있으므로 util은 근사치**.
- xe 드라이버 주파수 sysfs 경로(`tile*/gt*/freq*/act_freq`)의 실제 존재 여부.
- 통합 GPU에서 hwmon power/temp 노출 여부(세대 의존).

## 향후 확장 (미구현, 기록만)

- **Level Zero (oneAPI zes/Sysman)**: 정확한 정식 API지만 성숙한 Go 바인딩이 없어
  `libze_loader.so` 를 cgo로 직접 바인딩해야 함(초기 비용 큼). MVP 범위 제외.
  도입 시 sysfs/intel_gpu_top 를 대체하거나 보완하는 3순위 경로로 추가 가능.
- **엔진 세분화**: 현재는 `Render/3D` 대표값만 사용. Video/Blitter/Compute 등
  엔진별 사용률을 노출하려면 `GPUMetrics` 확장이 필요(계약 변경 → 리더 보고 사항).

## 계약 관련 메모 (변경 요청 아님, 참고)

- `types.go` 계약은 그대로 구현했고 변경하지 않았다.
- 통합 GPU에서 메모리 미지원을 `Supported[Field...]=false`(= 키 미표시)로 표현하는
  현 계약이 이 어댑터에 정확히 부합한다. 추가 계약 변경 필요 없음.
