# Frontend 구현 노트 (cli-tui-frontend)

> 작성: 2026-07-13. 대상 파일: `internal/render/snapshot.go`, `internal/render/tui.go`

## 상세 요구사항

### 필수 요구사항
- `Snapshot(w io.Writer, metrics []collector.GPUMetrics) error` — nvidia-smi 스타일 정렬 테이블 1회 출력 (계약 시그니처 준수).
- `RunTUI(ctx context.Context, collectors []collector.Collector, interval time.Duration) error` — bubbletea/lipgloss 실시간 대시보드 (계약 시그니처 준수).
- `IsSupported() == false` 필드는 모두 `N/A`로 표시 (0과 구분). 상수 `naValue = "N/A"` 한 곳에서 관리.
- 벤더 분기(`if vendor == ...`) 전무. 모든 값은 `GPUMetrics` + `Supported` 맵만으로 렌더링, `Vendor`는 표시 라벨로만 사용.
- 정렬: 벤더명 알파벳 → 인덱스. 동적 값(사용률 등)으로 정렬하지 않음 (스크립팅 안정성).
- TUI에서 한 벤더 `Collect()` 실패 시 해당 벤더 행만 stale 처리(마지막 성공값 유지 + dim + 하단 텍스트 경고), 나머지는 계속 갱신.
- 표준 라이브러리(`text/tabwriter`)만으로 스냅샷 정렬. bubbletea/lipgloss 외 의존성 추가 없음.

### 선택 요구사항
- TUI 하단 새로고침 주기 표시, altscreen 사용.
- 벤더 요약 헤더(감지 개수 + 벤더별 x카운트).
- 이름 미제공 시 NAME 컬럼도 `N/A`로 안전 처리.

## 레이아웃 결정

### 스냅샷 테이블 (snapshot.go)
- 컬럼: `VENDOR IDX NAME UTIL% MEM TEMP POWER CLOCK(G/M)` (스킬 예시와 동일).
- `text/tabwriter` (minwidth 0, tabwidth 4, padding 3, 좌측 정렬). 검증한 실제 출력:
  ```
  VENDOR   IDX   NAME                 UTIL%   MEM           TEMP   POWER   CLOCK(G/M)
  amd      0     AMD Instinct MI250   N/A     N/A           N/A    N/A     N/A
  intel    0     Intel Arc A770       0%      N/A           38C    N/A     300 MHz
  nvidia   0     NVIDIA RTX 4090      23%     4.1/24.0 GB   52C    120W    1800/10500 MHz
  ```
- MEM: `used/total GB` (byte / 1024^3, 소수 1자리). used·total 둘 다 supported여야 표시, 아니면 전체 `N/A`.
- CLOCK: graphics+memory 모두 supported → `G/M MHz`; graphics만 → `G MHz` (intel 통합 GPU); graphics 미지원 → `N/A`.
- 빈 슬라이스 → `No GPUs detected.` + nil 에러 (감지 GPU 0개는 정상 종료).
- `format*` 헬퍼(util/mem/temp/power/clock/name)는 스냅샷과 TUI가 **공유** → 두 뷰가 절대 어긋나지 않음.

### TUI 대시보드 (tui.go)
- 상단: `xpu-smi — N GPU(s) detected (amd x1, intel x1, nvidia x1)` 요약.
- 중단: 스냅샷과 동일한 8컬럼 테이블. 컬럼 폭은 plain 텍스트 기준으로 계산(`lipgloss.Width`)하여 ANSI 색상 코드가 정렬을 깨지 않게 함. 3칸 거터.
- 하단: `q/esc/ctrl+c: quit | refresh: <interval>` 키 안내 + stale 벤더 경고 라인.
- bubbletea Model/Update/View. `tea.Tick(interval)`으로 주기 갱신, `collectCmd`가 백그라운드 goroutine에서 전 벤더 재수집.

## 색상 임계값 (색상+텍스트 병행, 색상 단독 금지)
- UTIL% >= 80.0 → amber(214) 볼드. (`utilWarnPercent`)
- TEMP >= 85.0C → red(196) 볼드. (`tempWarnCelsius`)
- 임계값 색상은 해당 필드가 supported이고 stale이 아닐 때만 적용. stale 행은 전체 faint(dim) 처리.
- 색맹 대응: 색상은 강조일 뿐, 실제 숫자 텍스트가 항상 함께 표시됨.

## stale / 실패 처리
- 벤더별 `vendorState`{col, metrics(마지막 성공값), stale, lastErr, seen}로 상태 유지.
- 틱마다 `collectOne`이 각 collector를 방어적으로 실행: `Available()==false`, `Collect` 에러, **panic**(recover)까지 잡아 해당 벤더 result에만 담음 → 한 벤더가 대시보드 전체를 죽이지 않음.
- 실패 시: 마지막 성공 메트릭 유지 + stale 플래그 → 행 dim + 하단에 `! <vendor> stale: <reason>` (한 번도 성공 못 했으면 `unavailable`).

## 기타 결정
- `--interval` 하한 500ms 강제 (`minInterval`): 서브프로세스 기반 어댑터(rocm-smi 등) fork storm 방지 (스킬 지침).
- ctx 취소는 정상 종료로 간주 → `tea.WithContext(ctx)` 사용, 취소로 인한 program 에러는 nil 반환.
- 키바인딩: `q`, `esc`, `ctrl+c` 모두 종료. (벤더 필터/스크롤은 미구현 — 안내에도 표시하지 않음: "구현한 기능만 표시" 원칙.)

## 미검증 항목 (실 하드웨어/실행 필요)
- **TUI 실행 렌더링 미검증**: 이 개발 머신에 실 GPU 없음 + TTY 없음. bubbletea 프로그램 실측 렌더/색상/altscreen 동작은 미검증. Model/Update/View 로직과 컴파일(`go build`, `go vet`)만 확인.
- **stale 전환 실측 미검증**: 실제 벤더 어댑터의 간헐적 `Collect` 실패 시 dim/경고 라인 동작은 코드 경로상으로만 확인, 실행 미검증.
- **색상 임계값 트리거 미검증**: UTIL>=80 / TEMP>=85 실제 값에서의 색상 표시는 실 GPU 부하 없이는 미확인 (로직상 조건 분기만 확인).
- 스냅샷 테이블 정렬·N/A·MEM/CLOCK 포맷은 임시 in-package 테스트로 **실측 검증 완료**(위 예시 출력) 후 테스트 파일 제거.

## 검증 결과
- `go build ./internal/render/` — 통과.
- `go vet ./internal/render/` — 통과.
- `gofmt -l internal/render/` — 클린.
- 스냅샷 출력 임시 테스트 — 통과(스킬 예시와 동일 포맷 확인).
```
