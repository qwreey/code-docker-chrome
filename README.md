# code-docker-chrome

> **⚠️ 실험 단계 (2026-10).** 운영 환경에서 오래 써 보지 않았습니다. 전체 경로 테스트와
> 확장 개발 기반이 아직 남아 있습니다. 계획은
> [`.claude/backlog/next-pass-plan.md`](.claude/backlog/next-pass-plan.md)에 있습니다.

[code-docker](https://github.com/qwreey/code-docker)에 **Chrome을 물리기 위한 프로바이더**입니다.

컨테이너 안에서 Chrome(labwc + wayvnc)을 띄우고, 두 가지 경로로 내보냅니다.

- **CDP** → code-docker 안의 에이전트가 브라우저를 조작 (토큰 인증)
- **VNC** → router를 거쳐 사람이 같은 화면을 봄 (에이전트는 못 붙음)

Chrome은 code-docker와 다른 망에 있어서, 임의 사이트의 JavaScript가 code-server, webmanager,
dind에 닿지 않습니다. code-docker의 dev 서버는 포워딩한 포트만 Chrome에서
`http://localhost:<포트>`로 열립니다([아래](#dev-서버-보기-chrome-ports)).

같은 브라우저이므로, **사람이 VNC로 직접 로그인해 둔 세션을 그대로 Claude가 이어서 씁니다.**

## 단독 실행은 지원하지 않습니다

이 레포에는 `docker-compose.yml`이 없고 오버레이(`code-docker-chrome.yml`) 하나뿐입니다.
Chrome in Docker 이미지는 이미 좋은 게 여럿 있으니 또 만들 이유가 없고, 이 프로젝트의
존재 이유는 그 Chrome을 code-docker에 붙이는 배선 자체입니다. roblox-studio-docker가
standalone + 오버레이 두 벌을 유지하는 것과 다른 점입니다.

## 붙이기

code-docker에서 [`ootb.sh`](https://github.com/qwreey/code-docker)를 돌리다 "추가 프로젝트 git URL"이
나오면 이 레포 URL을 넣으면 끝입니다. 이미 설치된 배포는 `migrate.sh`의 같은 단계를 쓰세요.
clone, `extra-include.yml` 작성, router 대상 allowlist 등록, `CHROME_CDP_TOKEN` 생성까지 자동입니다.

`docker compose up -d` 다음, code-docker 안에서 한 번:

```sh
/run/code-docker-chrome/code-docker/install.sh
```

이 오버레이가 그 경로에 자기 `code-docker/`와 `cdp-bridge/`를 읽기 전용으로 넣어줍니다 —
설치 스크립트는 code-docker의 mise와 `reload-services`를 써야 해서 그 컨테이너 **안에서**
돌아야 하는데, 이 레포는 호스트의 배포 디렉터리(`builds/`)에 있고 code-docker가
마운트하는 어떤 경로에도 들어있지 않기 때문입니다. 호스트에서 바로 돌려도 됩니다:

```sh
docker exec -it code-docker /run/code-docker-chrome/code-docker/install.sh
```

cdp-bridge를 mise로 깔고, `cdp-unwrap` 서비스를 supervisord에 얹고, MCP 등록 명령을 안내합니다.
안내대로 등록하면 chrome-devtools-mcp가 `mcp-notice.py`를 거쳐 실행되고, 에이전트에게
`chrome-ports` 쓰는 법이 전달됩니다. 이전에 등록해 둔 것은 한 번 다시 등록하세요.

## dev 서버 보기 (chrome-ports)

Chrome 안의 `localhost`는 `chrome-front`로 풀리고, `chrome-front`는 포워딩 표에 있는 포트만
열어 대상으로 넘깁니다. code-docker 안에서:

```sh
chrome-ports add 5173              # Chrome의 http://localhost:5173 → code-docker:5173
chrome-ports add 8080 dind:8080    # dind 안 컨테이너가 게시한 포트
chrome-ports                       # 목록
chrome-ports rm 5173
```

- 주소는 반드시 `localhost`입니다(`127.0.0.1`, `code-docker`는 안 됨). 페이지 origin이
  `http://localhost:<포트>`라 보안 컨텍스트로 취급되고, Vite 같은 서버의 호스트 검사도
  통과합니다.
- 포워딩한 포트는 Chrome이 여는 **모든 페이지**에서 닿습니다. 다 쓴 것은 지우세요.
- 같은 이유로 대상은 code-docker와 dind의 dev 서버로 제한됩니다. code-docker의 nginx(80),
  WebDAV(82), dind의 Docker API(2375/2376)는 IP로 적어도 거절됩니다.
- 같은 표를 webmanager의 "Chrome ports" 화면에서도 보고 고칠 수 있습니다.
- 표는 `chrome-front` 볼륨에 저장되어 재생성 후에도 남습니다. 1024 미만 포트는 안 됩니다.

## VNC 화면

roblox-studio-docker와 같은 작은 데스크톱입니다.

- 아래 taskbar: **☰ Menu**(Chromium, Thunar), 창 목록(최소화한 창은 여기서 다시 엽니다), 시계.
- 바탕화면 우클릭 메뉴: 새 Chromium 창, 파일 관리자(Thunar).
- 창 닫기는 제목줄 X나 Alt+F4. labwc 기본 단축키(Alt+Tab, Super+방향키)도 그대로입니다.
- Chrome의 마지막 창을 닫으면 곧바로 새로 뜹니다. 에이전트가 쓰는 브라우저라 항상 하나는 떠 있습니다.
- 다운로드는 `/root/Downloads`로 갑니다. 컨테이너 안이라 **재생성하면 사라집니다.**

## 구조

```
[code-docker]                     [chrome-front]                [code-docker-chrome]
chrome-devtools-mcp
  --browser-url http://127.0.0.1:9222
        ▼
  cdp-bridge unwrap (loopback)
    + Bearer $TOKEN ──내부망──▶ chrome-cdp:9223 ──chrome-net──▶ cdp-bridge wrap (chrome-browser:9223)
                                                                 Bearer 검증 → Chrome (127.0.0.1:9222)
  dev 서버 :5173 ◀──내부망── 포워딩 표의 포트만 ◀──chrome-net── Chrome의 http://localhost:5173
  chrome-ports ──내부망──▶ :8090 관리 API·페이지                    │
                                                  chrome-vnc:5900 ──┴──▶ code-docker-router ──▶ 사람
```

**Host 헤더가 양쪽 프록시를 그대로 통과하는 것이 핵심입니다.** Chrome은
`webSocketDebuggerUrl`을 요청에 실린 Host에 맞춰 재작성하므로, MCP는 CDP가 완전히
로컬에 있다고 인식하고 원격용 설정이 전혀 필요 없습니다.

### 망 구성

| 네트워크 | 참여자 | 성격 |
|---|---|---|
| `code-docker-chrome-net` (`internal: true`) | chrome, chrome-front, router(게이트웨이) | Chrome의 작업망. code-docker·dind는 없음 |
| `code-docker-internal` | chrome-front, code-docker, dind, ... | Chrome은 안 붙음. `chrome-front`만 `chrome-cdp` 별칭으로 붙음 |
| `code-docker-chrome-vnc` (`internal: true`) | chrome, router | VNC 전용 격리망 |

Chrome은 임의 사이트의 JavaScript를 돌리므로, code-docker-internal에 있으면 그 페이지들이
인증 없는 `dind:2375`와 code-docker의 nginx에 닿습니다. 그래서 두 망 사이에는
`chrome-front` 하나만 두고, CDP(code-docker → Chrome)와 포워딩한 dev 포트(Chrome →
code-docker/dind)만 넘깁니다. 관리 API는 code-docker 쪽 망에만 열려 있어서, Chrome이 연
페이지가 포워딩을 바꿀 수 없습니다.

## cdp-bridge를 고쳤다면

같은 `cdp-bridge/main.go`가 **두 번, 서로 다른 곳에서** 빌드됩니다.

| 어느 쪽 | 언제 | 어디서 | 결과물 |
|---|---|---|---|
| `wrap` | `docker compose build code-docker-chrome` | Dockerfile 1단계, `golang:1.27-alpine` | 이미지 안 `/usr/local/bin/cdp-bridge` |
| `unwrap` | `install.sh` 실행 | 돌아가는 code-docker 안, mise의 go 1.27 | `/code/.local/bin/cdp-bridge` |

`unwrap` 쪽이 읽는 소스는 오버레이가 마운트한 **작업 트리 실시간**이고, `wrap` 쪽은
**이미지 빌드 당시의 스냅샷**입니다. 그래서 소스를 고치면 둘 다 해줘야 합니다:

```sh
docker compose build code-docker-chrome && docker compose up -d code-docker-chrome
docker exec -it code-docker /run/code-docker-chrome/code-docker/install.sh
```

한쪽만 하면 두 절반의 버전이 갈립니다. 실질적으로는 거의 문제가 안 됩니다 — 둘이
주고받는 건 진화하는 와이어 프로토콜이 아니라 HTTP 리버스 프록시 + Bearer 헤더
하나뿐이라 깨질 지점이 없습니다. 그래도 디버깅할 때 헷갈릴 수 있으니 알아두세요.

## 알아둘 것

- **Chrome은 `--no-sandbox`로 돕니다.** 컨테이너가 root로 돌고, Chrome은 root에서
  샌드박스 기동을 거부합니다(crbug.com/638180). Docker 기본 seccomp가
  `clone(CLONE_NEWUSER)`를 막는 것도 별개로 걸립니다. desktop+VNC형 이미지의 표준
  선택이고(linuxserver/docker-chromium도 하드코딩), 경계는 컨테이너와 위 두 망이 집니다.
- **CDP 포트에 닿는 것은 브라우저 완전 장악**과 같습니다. 프로필에 로그인된 모든 사이트의
  쿠키를 포함해서요. `CHROME_CDP_TOKEN`이 유일한 게이트이므로 유출되지 않게 두세요.
- `/dev/dri`는 넘기지 않습니다. 소프트웨어 렌더링으로 충분하고, 렌더 노드는 호스트 커널로
  직행하는 큰 ioctl 표면입니다.

## 문제가 생기면

**`Host header is specified and is not an IP address or localhost.`**
Chrome DevTools 엔드포인트의 DNS 리바인딩 방어입니다. `chrome-cdp:9223`을 서비스 이름으로
직접 치면 토큰이 맞아도 이게 나옵니다 — Host가 그대로 전달되는 구조라서(그게 ws URL이
로컬로 돌아오게 만드는 원리입니다) Chrome이 호스트네임 Host를 거부하는 것뿐이고,
정상 경로인 unwrap 경유(`127.0.0.1:9222`)에서는 발생하지 않습니다. 손으로 확인하려면:

```sh
curl -H "Host: 127.0.0.1:9222" -H "Authorization: Bearer $CHROME_CDP_TOKEN" \
     http://chrome-cdp:9223/json/version
```

**`cdp-bridge: CHROME_CDP_TOKEN is empty — refusing to start`**
양쪽 다 빈 토큰이면 기동을 거부합니다. wrap에서 빈 토큰은 아무나 통과시키고, unwrap에서는
헤더는 붙어 있는데 401이 나서 원인이 안 보이기 때문입니다. code-docker 쪽이라면
오버레이가 값을 병합해주므로, 비어 있다는 건 보통 `EXTRA_INCLUDE`가 안 켜졌거나 컨테이너가
그 전에 만들어졌다는 뜻입니다.

**Chrome에서 `localhost:<포트>`가 안 열림**
`chrome-ports`에 그 포트가 있는지, 상태가 `target unreachable`이 아닌지 보세요. 대상
서버가 루프백(`127.0.0.1`)에만 바인딩돼 있으면 `chrome-front`가 닿지 못합니다
(`0.0.0.0`으로 띄우세요). `127.0.0.1:<포트>`나 `code-docker:<포트>`로 연 것은 원래 안 됩니다.

**`resolve_bind_alias: '...' did not resolve`**
그 별칭을 정의하는 네트워크에 컨테이너가 안 붙어 있습니다. 0.0.0.0으로 폴백하지 않고 죽는 게
의도입니다 — 조용히 폴백하면 CDP가 VNC망에도, 화면이 에이전트망에도 열립니다.
