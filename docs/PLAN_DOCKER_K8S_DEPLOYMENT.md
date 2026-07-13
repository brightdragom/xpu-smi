# xpu-smi 컨테이너/쿠버네티스 배포 개발 계획

> 작성: 2026-07-14 · 상태: **계획 단계 (미착수)**

## 배경/목표

현재 xpu-smi는 단일 노드(로컬 머신)에서 직접 실행하는 CLI/TUI 도구다. 이를 Docker 컨테이너로 패키징하고 Kubernetes에 파드 형태로 배포하여, 클러스터의 각 노드에 장착된 Nvidia/AMD/Intel GPU 상태를 중앙에서 모니터링할 수 있도록 확장한다.

특정 파드에 할당된 GPU 리소스만 보는 것이 아니라 **노드 전체의 GPU**를 봐야 하므로, 배포 단위는 일반 Deployment/Pod가 아니라 노드마다 1개씩 뜨는 **DaemonSet**을 전제로 한다.

## 상세 요구사항

### 필수 요구사항
- xpu-smi를 실행하는 Dockerfile 작성 (멀티스테이지 빌드: Go 빌드 스테이지 + 경량 런타임 스테이지)
- 컨테이너 내부에서 각 벤더 디바이스에 접근 가능하도록 구성
  - Nvidia: `/dev/nvidia*` 디바이스 및 `libnvidia-ml.so`(NVIDIA Container Toolkit 경유) 노출
  - AMD: `/dev/kfd`, `/dev/dri` 디바이스 노출
  - Intel: `/dev/dri` 디바이스 노출 + `intel_gpu_top` 실행을 위한 `CAP_PERFMON` capability(또는 필요 최소 권한)
- 기존 스냅샷/TUI 모드 대신(또는 추가로) **HTTP 메트릭 엔드포인트(`/metrics`, Prometheus exposition format)** 모드 추가 — TUI는 컨테이너 환경에서 스크래핑 대상이 될 수 없으므로, 클러스터 모니터링과 연동하려면 exporter 형태가 필요
- Kubernetes DaemonSet 매니페스트 작성 — 노드마다 1개 파드, hostPath 볼륨으로 필요한 `/dev`, `/sys/class/drm` 마운트
- 기존 `Collector`/`GPUMetrics`/`Supported` 계약을 그대로 재사용 (벤더 무관 설계를 컨테이너 환경에서도 유지, 벤더별 분기 추가 금지 원칙 유지)
- 벤더 드라이버/디바이스가 없는 노드에서도 파드가 크래시 없이 정상 기동 (기존 `Available()==false` 안전 설계를 컨테이너 환경에서도 검증)

### 선택 요구사항
- Helm 차트로 패키징 (values.yaml로 네임스페이스, 리소스, nodeSelector 등 커스터마이징)
- Prometheus ServiceMonitor 리소스 자동 등록 (kube-prometheus-stack 연동)
- Grafana 대시보드 JSON 템플릿 제공
- 비-privileged 최소 권한 구성 옵션 (가능한 벤더/드라이버 조합에 한해 `privileged: true` 없이 동작하는 경로 조사)
- 멀티 아키텍처 이미지 빌드(amd64/arm64) 및 컨테이너 레지스트리 배포 자동화(CI)
- 노드별 GPU 인벤토리를 클러스터 전체 관점에서 집계하는 중앙 API/대시보드 (다중 노드 집계, 기존 `DEVELOPMENT_PLAN.md`의 "다중 노드/클러스터 수집" 선택 요구사항과 연계)

## 설계 방향 (초안)

### 배포 아키텍처
```
[Node A] --- DaemonSet Pod (xpu-smi --exporter) --- Prometheus scrape
[Node B] --- DaemonSet Pod (xpu-smi --exporter) --- Prometheus scrape
[Node C] --- DaemonSet Pod (xpu-smi --exporter) --- Prometheus scrape
                        │
                  Prometheus/Grafana (클러스터 중앙)
```

### 주요 트레이드오프
| 항목 | 선택지 | 비고 |
|------|--------|------|
| 권한 | `privileged: true` vs 세부 `securityContext.capabilities` | Intel `intel_gpu_top`는 `CAP_PERFMON` 필요, sysfs/디바이스 접근 범위에 따라 privileged가 더 단순하지만 보안상 비선호 |
| 출력 형태 | 기존 TUI 유지 + exporter 모드 추가 vs exporter 전용 빌드 | 로컬 디버깅 시 TUI도 유용하므로 겸용 권장 |
| 배포 단위 | DaemonSet vs 별도 GPU 노드 전용 Deployment | 노드 전체 가시성 확보를 위해 DaemonSet이 기본 |

### 예상 변경 파일 (착수 시)
- `Dockerfile` (신규)
- `deploy/k8s/daemonset.yaml`, `deploy/k8s/rbac.yaml` 등 (신규)
- `internal/render/exporter.go` (신규 — Prometheus exposition format 출력)
- `cmd/xpu-smi/main.go` (`--exporter`/`--metrics-port` 플래그 추가)
- `docs/DEVELOPMENT_PLAN.md` 및 `README.md`에 배포 가이드 반영

## 검증 (착수 시 기준)
- 각 벤더 디바이스가 없는 노드에서 파드가 CrashLoopBackOff 없이 정상 기동하는지 (기존 `Available()==false` 안전 설계 유지 확인)
- `/metrics` 엔드포인트가 Prometheus exposition format을 준수하는지
- DaemonSet이 GPU 미보유 노드에도 스케줄되어도 문제없이 "감지된 GPU 없음" 상태로 동작하는지, 혹은 nodeSelector/toleration으로 GPU 노드만 타겟팅할지 결정
- 최소 권한(비-privileged) 구성 시도 시 실제로 필요한 capability 집합이 문서화되어 있는지

## 메모
- 이 문서는 **계획만** 기록한 것이며, 실제 구현은 아직 시작하지 않았다.
- 착수 시 이 문서를 오케스트레이터 입력으로 사용해 하네스 팀(또는 신규 배포 전용 에이전트)을 구성할지, 기존 팀을 확장할지 결정 필요.
