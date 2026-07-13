# xpu-smi 개발 계획

> 최종 갱신: 2026-07-13 · 상태: **1차 개발 완료 (MVP, QA 통과)**

## 배경/목표

GPU 벤더(Nvidia/AMD/Intel)에 제약받지 않고 단일 노드의 GPU 리소스(사용률·메모리·온도·전력·클럭)를 한 화면에서 파악하는 통합 모니터링 CLI/TUI 도구를 Go로 개발한다. 벤더별 SDK 차이를 하나의 `Collector` 인터페이스 뒤에 숨기는 것이 핵심 설계 목표다.

## 상세 요구사항

### 필수 요구사항
- Go 단일 바이너리로 빌드/배포 가능해야 한다
- 벤더 중립 `Collector` 인터페이스와 `GPUMetrics` 데이터 모델을 정의한다 (단위 고정: bytes/celsius/watts/MHz)
- Nvidia 어댑터: NVML(go-nvml) 연동, 실패 시 `nvidia-smi` CLI 파싱 폴백
- AMD 어댑터: `amd-smi` → `rocm-smi` → sysfs 순서의 런타임 폴백 체인
- Intel 어댑터: `intel_gpu_top -J` + sysfs(i915/xe) 조합
- 각 어댑터는 해당 벤더 드라이버/SDK가 없는 머신에서 패닉 없이 `Available() == false`로 자기 비활성화해야 한다
- 벤더가 구조적으로 지원하지 않는 메트릭은 0이 아닌 `Supported` 플래그(false)로 구분하고 UI에서 `N/A`로 표시한다
- 스냅샷 모드(기본): nvidia-smi 스타일 테이블 1회 출력 후 종료
- TUI 모드(`--watch`): 실시간 갱신 대시보드 (기본 1초, `--interval` 조정 가능)
- 렌더링 코드에 벤더별 분기(`if vendor == ...`)를 두지 않는다
- 한 벤더/한 GPU의 수집 실패가 전체 출력 실패로 번지지 않아야 한다
- 모니터링 범위: 단일 노드(로컬 머신)

### 선택 요구사항
- `--json` 출력 모드 (스크립팅/외부 연동용)
- 벤더/인덱스 필터 옵션
- Intel Level Zero(Sysman) cgo 바인딩 연동 (MVP는 intel_gpu_top/sysfs)
- 프로세스별 GPU 사용량 표시
- 다중 노드/클러스터 수집 (에이전트-서버 구조로 확장)

## 설계

### 아키텍처
- **하네스**: 에이전트 팀 모드 (gpu-architect → nvidia/amd/intel-backend + cli-tui-frontend 병렬 → qa-integrator 점진 검증)
- **패턴**: 파이프라인(인터페이스 선행 확정) + 팬아웃/팬인(벤더 어댑터 병렬 구현) + 생성-검증(QA)
- 어댑터 등록: `collector.Register(factory)` 명시 등록 → `collector.Detect()`가 `Available() == true`인 어댑터만 활성화

### 파일 구조
```
xpu-smi/
├── go.mod
├── cmd/xpu-smi/main.go            # 플래그 파싱, Detect(), 렌더러 선택
├── internal/
│   ├── collector/
│   │   ├── types.go               # Collector 인터페이스, GPUMetrics
│   │   ├── registry.go            # Register/Detect
│   │   ├── nvidia/nvidia.go
│   │   ├── amd/amd.go
│   │   └── intel/intel.go
│   └── render/
│       ├── snapshot.go            # 1회 스냅샷 테이블
│       └── tui.go                 # --watch 대시보드
├── docs/DEVELOPMENT_PLAN.md       # 이 문서
└── _workspace/                    # 설계 문서·구현 노트·QA 리포트 (감사 추적용)
```

## 변경 파일
- 신규: 위 파일 구조 전체 (프로젝트가 빈 상태에서 시작)

## 검증
- `go build ./...` 전체 빌드 성공
- QA(qa-integrator)의 계약↔구현 교차 검증: 단위 변환(NVML mW→W, sysfs milli-C→C 등), `Supported` 플래그 처리, 레지스트리 등록 누락, 렌더러의 N/A 처리
- 개발 환경에 실 GPU가 없으므로 "감지된 GPU 없음" 정상 종료 경로를 실행으로 확인, 하드웨어 의존 항목은 "미검증"으로 명시

## 검증 결과 (2026-07-13)

- `go build ./...` / `go vet ./...` 전부 exit 0, `gofmt` 클린 (go-nvml 내부 cgo deprecation 경고만 존재, 무해)
- 바이너리 실행: GPU 미탑재 개발 머신에서 "감지된 GPU 없음" 출력 후 exit 0, 패닉 없음
- QA 교차 검증: **PASS 21 / FAIL 0 / 미검증 6종** (`_workspace/03_qa_report.md`)
  - 단위 변환 3지점(NVML mW→W, sysfs milli-℃→℃, µW→W) 전수 확인 — 누락/오배수 없음
  - 레지스트리 3개 어댑터 등록, 렌더러 IsSupported 게이팅/벤더 분기 없음 확인
- 미검증(실 하드웨어 필요): amd-smi/rocm-smi·intel_gpu_top 실제 JSON 스키마, TUI 실 렌더링, 멀티 GPU 격리 — 실 GPU 장비에서 후속 확인 필요

## 실행 이력

| 날짜 | 내용 |
|------|------|
| 2026-07-13 | 하네스 구성 완료 (에이전트 6, 스킬 5, 오케스트레이터 1) |
| 2026-07-13 | 개발 계획 수립, Notion 동기화, 개발 착수 |
| 2026-07-13 | 아키텍처 확정 (Collector 인터페이스, 레지스트리, 골격) 및 의존성 확보 |
| 2026-07-13 | 4개 에이전트 병렬 구현 완료 — nvidia(NVML), amd(amd-smi→rocm-smi→sysfs), intel(intel_gpu_top+sysfs, 통합 GPU N/A 처리), frontend(스냅샷+TUI) |
| 2026-07-13 | main.go 배선, QA 교차 검증 PASS 21/FAIL 0, 최종 빌드·실행 확인 — **MVP 완료** |
| 2026-07-14 | 사용자 매뉴얼 `README.md` 작성 (개요/설치/사용법/출력 읽는 법/벤더별 동작/문제 해결/제한사항/아키텍처) |
