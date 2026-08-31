# code-docker-chrome

[code-docker](https://github.com/qwreey/code-docker)에 **Chrome을 물리기 위한 프로바이더**입니다.

컨테이너 안에서 Chrome(labwc + wayvnc)을 띄우고, 두 가지 경로로 내보냅니다.

- **CDP** → code-docker 안의 에이전트가 브라우저를 조작 (토큰 인증)
- **VNC** → router를 거쳐 사람이 같은 화면을 봄 (에이전트는 못 붙음)

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

## 구조

```
[code-docker]                                  [code-docker-chrome]
chrome-devtools-mcp
  --browser-url http://127.0.0.1:9222
        │  Host: 127.0.0.1:9222
        ▼
  cdp-bridge unwrap (loopback)                 cdp-bridge wrap (chrome-cdp:9223)
    + Authorization: Bearer $TOKEN  ──내부망──▶   Bearer 검증 → 헤더 제거
                                                        ▼
                                                 Chrome (127.0.0.1:9222)
                                                 labwc + wayvnc
                                                        │
                                    chrome-vnc:5900 ────┴──▶ code-docker-router ──▶ 사람
```

**Host 헤더가 양쪽 프록시를 그대로 통과하는 것이 핵심입니다.** Chrome은
`webSocketDebuggerUrl`을 요청에 실린 Host에 맞춰 재작성하므로, MCP는 CDP가 완전히
로컬에 있다고 인식하고 원격용 설정이 전혀 필요 없습니다.

### 네트워크가 두 개인 이유가 서로 다릅니다

| 네트워크 | 참여자 | 성격 |
|---|---|---|
| `code-docker-internal` | chrome, code-docker, dind, ... | 기존 망을 **그대로 씀**. CDP는 토큰으로 막음 |
| `chrome-vnc` (`internal: true`) | chrome, router | VNC 전용 **격리망**. code-docker는 안 붙음 |

CDP용 전용 망을 만들지 않은 것은 의도적입니다 — 그러려면 code-docker가 그 망에 붙어야
하고, 프로바이더가 메인의 토폴로지를 고치는 셈이 됩니다. 토큰이 같은 결과를 냅니다.
반대로 VNC망은 **빼기**라서(code-docker를 제외하는 것) 그 문제가 없습니다.

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

**`resolve_bind_alias: '...' did not resolve`**
그 별칭을 정의하는 네트워크에 컨테이너가 안 붙어 있습니다. 0.0.0.0으로 폴백하지 않고 죽는 게
의도입니다 — 조용히 폴백하면 CDP가 VNC망에도, 화면이 에이전트망에도 열립니다.
