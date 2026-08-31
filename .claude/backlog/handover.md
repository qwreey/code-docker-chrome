# 핸드오버 — 2026-08-31

이 레포가 만들어진 세션의 인수인계 노트. 코드에 안 남는 것(라이브 배포 상태, 손으로
넣은 설정, 검증한 것과 안 한 것의 경계)만 적는다. 설계 근거는 `CLAUDE.md`에 있다.

## 지금 어디까지 왔나

`~/Projects/code-docker` 에서 실제로 스택을 띄워 **끝까지 동작을 확인했다**.
code-docker 안의 `chrome-devtools-mcp`가 다른 컨테이너의 Chrome을 router 경유로 몰아
외부 사이트를 열고 DOM을 읽었다.

커밋:

- `code-docker` — `a431928` (main, 푸시됨): `/code` 볼륨 위 supervisord include +
  `bin/reload-services`. 이 레포의 `install.sh`가 이것에 의존한다.
- `code-docker-chrome` — `b4b01db` → `1676899` (main, 푸시됨).

## 검증된 것

| 항목 | 근거 |
|---|---|
| 컨테이너 7개 프로그램 기동 | 라이브 `supervisorctl status` 전부 RUNNING |
| netinit 대기 | 로그: `waiting...` → `default route present (172.19.0.5)` → 계속 |
| 바인딩 격리 | `127.0.0.1:9222`(Chrome) / `<internal IP>:9223`(CDP) / `<vnc IP>:5900`(VNC), `0.0.0.0` 없음 |
| 토큰 게이트 | **dind에서 IP로 직접 → 401** (이름 해석은 아예 실패). 위협 모델대로 |
| VNC 격리 | internal 망에서 5900 연결 불가 |
| router → VNC | `RFB 003.008` 배너 응답, 화면 `HEADLESS-1 1920x1080` |
| CDP 왕복 | code-docker 안에서 `ws://127.0.0.1:9222/devtools/...` |
| MCP 실조작 | `navigate_page` → example.com, `evaluate_script` → `"Example Domain @ example.com"` |
| `install.sh` | 라이브에서 3회 실행, 전역 mise 설정 무변경 확인 |
| compose 병합 | roblox 오버레이와 공존, `PREFIX=test-`로 전 이름 격리, 토큰 미설정 시 명확한 실패 |
| 컨테이너 recreate | `chromium` 정상 재기동 + CDP 유지 (`d6f5d74`의 Singleton 정리 이후) |

## 아직 안 된 것

1. **router VNC 탭에 대상 등록** — allowlist·네트워크·RFB 도달성까지 전부 깔렸다.
   남은 건 router-manager 웹 UI에서 `chrome-vnc:5900`을 대상으로 추가하는 런타임
   작업뿐. 이걸 해야 사람이 실제로 화면을 본다. **아직 아무도 이 브라우저 화면을
   눈으로 못 봤다** — wayvnc가 RFB 배너를 주는 것까지만 확인했다.
2. **`ootb.sh` 자동 연동 흐름** — 매니페스트를 읽고 검증만 했고 실제로 돌리진 않았다.
   아래 "손으로 넣은 설정"이 곧 ootb가 해줘야 할 일 목록이다.
3. **chrome 컨테이너 IP가 바뀌었을 때의 unwrap 재연결** — recreate 자체는 검증했다
   (컨테이너를 두 번 재생성했고 unwrap은 재시작 없이 CDP를 계속 서빙했다). 다만 두
   번 다 IP가 `172.19.0.2`로 그대로여서 **IP 변경 경로는 아직 안 탔다**. Go transport가
   죽은 커넥션을 재다이얼하므로 될 것으로 보이나 확인은 안 됨.
4. `studio` 서비스는 이번 검증과 무관해서 안 올렸다 (`docker compose up -d studio`).

## 손으로 넣은 설정 (ootb가 대신 해줘야 할 것들)

전부 gitignore 대상이라 레포에는 안 들어간다. `~/Projects/code-docker/`:

```
.env         + CHROME_CDP_TOKEN=<랜덤 32바이트 hex>
extra-include.yml  + "  - path: ../code-docker-chrome/code-docker-chrome.yml"
.env.router  ROUTER_EXTRA_ALLOWED_TARGET_HOSTS=vnc-only → vnc-only,chrome-vnc
```

빌드 컨텍스트를 지정하는 변수는 없다. compose는 include된 fragment의 상대경로를 그
fragment 자신의 디렉터리 기준으로 푸는데(실측), 이 오버레이는 항상 레포 루트에 있으므로
`context: .`이 어디에 클론하든 맞는다. 초기 버전엔 `CHROME_BUILD_CONTEXT`가 있었지만
기본값이 `builds/code-docker-chrome`이라 ootb 경로에서 `<레포>/builds/code-docker-chrome`
으로 풀려 깨졌다 — 이 배포가 형제 체크아웃이라 우연히 양쪽 해석이 같은 경로를 가리켜서
안 드러났을 뿐이다.

원본 백업은 `/tmp/claude-1000/.../scratchpad/livebak/` 에 뒀는데 **`/tmp`라 재부팅하면
사라진다**. 되돌릴 일이 있으면 위 표를 보고 직접 지우는 편이 확실하다.

## 세션 중 오염시켰다가 되돌린 것

`install.sh` 초기 버전이 `mise use -g`를 써서 code-docker의
`~/.config/mise/config.toml`에 `go`와 `node`를 넣었다. `go`는 `mise unuse -g go`로
제거했고, 스크립트도 `mise exec`로 고쳤다(`1676899`). **`node = "lts"`는 남겨뒀다** —
`npx chrome-devtools-mcp`가 Claude Code에 의해 나중에 실행되므로 PATH에 실제로 있어야
한다. 원래 없던 항목이라는 점은 알고 있어야 한다.

## 조사 기록

설계에 이르기까지의 실측(공식 Claude in Chrome이 왜 안 되는지, Chrome 샌드박스 매트릭스,
CDP가 `--remote-debugging-address`를 무시하는 것, Host 재작성 동작 등)은
`~/Desktop/research/chrome-mcp-remote.md` 부록 A~C에 있다. 이 레포 밖이고 git에도 없다.
설계를 뒤집으려 하기 전에 읽어볼 것 — 대부분의 "왜 이렇게 안 했지"는 거기서 이미
측정해서 기각한 것들이다.
