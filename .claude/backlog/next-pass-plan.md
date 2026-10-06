# 다음 작업 묶음 (백로그, 2026-09-28 작성)

한 번에 몰아서 할 작업 목록. 아직 착수 전이다. 사용자 메모: "VNC 바닥에 taskbar도 없고,
studio-docker 쪽 변경도 싱크가 안 된 상태라 나중에 한 번 작업해야 한다 — 테스트도
제대로 못 했다".

## 1. roblox-studio-docker에서 가져와야 할 변경 (싱크 누락)

**완료 (2026-10-07).** taskbar, wofi 토글, labwc 기본 바인딩, Thunar를 가져왔고, 메뉴와
xdg-open에서 Chrome을 여는 `chromium-window`, 제목줄 최소화/최대화 버튼을 더했다.
지금 구조는 `CLAUDE.md`의 "The desktop"이 기준이다. 공통 서비스 스크립트(dbus, wayvnc,
watchdog, dns-local, wait-for-wayland)는 대조해 보니 이쪽이 이미 따로 정리돼 있어 가져올 게
없었고, 나머지 차이(pointer-warp, desktop-resize, noVNC)는 Studio 전용이다. 아래는 당시 목록이다.

이 레포는 roblox-studio-docker의 관례를 따라 만들었지만(`CLAUDE.md` 참고), 그쪽이
그 뒤에 고친 것들이 여기엔 없다. 확인할 커밋(roblox-studio-docker):

- `1411ac8` Add a waybar taskbar + wofi app launcher on top of labwc — **VNC 화면
  하단 taskbar가 없는 문제의 원인.** 여기도 같은 방식으로 붙일 것.
- `409b5b6` fix(taskbar): toggle wofi from the menu button instead of stacking instances
- `f01d964` fix(wm): restore labwc's default mouse/key bindings, wiped by one mousebind
  — labwc `rc.xml`에 mousebind를 하나라도 넣으면 기본 바인딩이 통째로 사라지는 문제.
  여기 `config/wm/`도 같은 함정이 있는지 확인.
- `ca11b96` feat!: rename the VNC alias vnc-only to roblox-studio-vnc — 여기는 이미
  `chrome-vnc`라 이름 문제는 없지만, 그 커밋이 같이 바꾼 문서/라우터 쪽 가정이 있는지
  훑어볼 것.

그 밖에 `git -C ../roblox-studio-docker log --since=2026-08-31 -- config entrypoint.sh`로
공통 부분(`config/supervisor/`, `config/wm/`, entrypoint)의 차이를 한 번 전부 대조한다.

## 2. 테스트를 제대로 한 번 하기

`handover.md`에 적힌 검증은 CDP 경로(에이전트가 외부 사이트 DOM 읽기)까지다. 다음은
확인되지 않았다:

- VNC로 사람이 보면서 같은 세션을 조작하는 경로 전체(router VNC 탭 → chrome-vnc)
- 컨테이너 재생성 후 프로필/로그인 유지 (`d6f5d74` 프로필 락 수정 이후 재확인 포함)
- 1번 싱크 이후의 taskbar/launcher 동작

## 3. 망 분리 — Chrome이 `code-docker-internal`에 직접 붙어 있는 문제

**완료 (2026-10-06).** 아래 계획과 달리 `cdp-bridge wrap`을 떼지 않고 `chrome-front`(Go) 하나가
CDP 중계, dev 포트 포워딩(동적 표), 관리 API를 맡는 모양이 됐다. dev 서버 경로는 고정 포트
목록 대신 `chrome-ports`로 에이전트가 관리하고 Chrome에서는 `localhost`로 연다. 지금 구조는
`CLAUDE.md`의 "Three networks"와 "chrome-front and `localhost`"가 기준이고, 아래는 당시 조사 기록이다.

code-docker-firecrawl을 만들며 정리한 위협 모델 (code-docker의
`.claude/archive/repo-restructure-plan-done.md` 이후 firecrawl 작업 참고):

- 이 Chrome은 에이전트가 **임의 사이트의 JS를 실행**하는 브라우저인데
  `code-docker-internal`에 직접 붙어 있다. 그 망에는 인증 없는 `dind:2375`(Docker API
  — authz가 privileged는 막지만 `/code/` bind mount는 허용 → `~/.claude` 크리덴셜,
  ssh 키 노출 가능), 기본값으로 로그인 없는 webmanager(`code-docker:81`, authgate는
  opt-in), router-manager가 있다.
- firecrawl은 이 이유로 전용망(router만 egress 게이트웨이) + 한 지점 브리지 구조로
  갔다. Chrome만 예외로 두면 반만 닫은 셈이다.
- 방향: Chrome 본체는 전용망(`netinit.gateway` = router)에만 두고, `code-docker-internal`
  에는 `cdp-bridge wrap`만 붙인다(이미 bearer 토큰 게이트가 있으니 브리지 역할에 맞다).
  VNC는 지금처럼 `chrome-vnc` 격리망 + router.
- **실측 결과 (2026-09-28, 이 컨테이너의 Chromium 151):** `https://example.com`
  페이지에서 `fetch('http://<code-docker-internal IP>:port/')`(no-cors, cors 둘 다)는
  8초 타임아웃까지 **보류**되고 서버엔 요청이 한 번도 도착하지 않았다. 같은 주소를 직접
  내비게이션하거나 사설 origin 페이지에서 부르면 정상 도착 — 즉 Local Network Access가
  공개→사설 요청을 **권한 프롬프트 뒤에** 붙잡는다(판정이 목적지 IP 기준이라 DNS
  rebinding도 같은 벽에 걸림). 게다가 보류된 프롬프트가 탭에 남아 다음 내비게이션까지
  막았다.
  - 그래서 우선순위는 내려가지만 0은 아니다: 막는 게 "거부"가 아니라 **사람 클릭 한 번**
    이다. VNC로 보는 사람이 "허용"을 누르거나, 에이전트가 CDP로 권한을 주면(
    `Browser.grantPermissions`) 그 origin은 프로필에 허용으로 남는다.
  - 참고: 그 사이 code-docker 쪽이 바뀌어 code-server/webmanager는 이제 loopback에만
    열리고(nginx :80만 망에 노출), router 앞문도 이 망에서 닫혔다. 이 망에서 여전히
    인증 없이 닿는 큰 것은 `dind:2375`와 code-docker nginx(:80)다.

- **결정 근거 정리 (2026-10-04 조사, 권장: 착수 시 1순위로 처리)**
  - LNA는 네트워크 경계가 아니라 사람이나 CDP가 풀 수 있는 권한 프롬프트다. 허용은
    프로필에 남는다. 이 Chromium은 root + `--no-sandbox`라 렌더러가 뚫리면 LNA 자체를
    건너뛴다. "닿지 않음"만이 믿을 수 있는 벽이다.
  - 대체 수단으로 쓰면 안 되는 것: `LocalNetworkAccessAllowedForUrls` 정책,
    `--disable-features=LocalNetworkAccessChecks`. 둘 다 권한을 **여는** 쪽이고, 플래그는
    내부용이라 언제든 사라질 수 있다.
    - LNA 일정: Chrome 138 플래그 미리보기, 142부터 강제, 145에서 `local-network` /
      `loopback-network`로 나뉨, 146부터 WebRTC에도 적용.
    - 출처: developer.chrome.com/blog/local-network-access,
      chromestatus 5068298146414592,
      learn.microsoft.com/ecdn/how-to/configure-local-network-access-policy
  - 선례:
    - Selenium Grid가 노출된 채 탈취됐다(SeleniumGreed, 3만 개 이상, wiz.io).
    - Playwright Docker 문서는 "신뢰하지 않는 사이트 방문용 아님"이라고 쓴다.
    - Anthropic computer-use 데모는 전용 VM과 네트워크 제한을 권한다.
    - 격리를 기본으로 주는 이미지는 찾지 못했다(browserless/neko는 미확인).
  - 바뀌는 구성:
    - Chrome 본체는 전용 망에만 둔다(`netinit.gateway` = router).
    - `cdp-bridge wrap`을 별도 컨테이너로 떼어, 그것만 전용 망과 `code-docker-internal`에
      양다리를 걸친다.
    - VNC는 지금처럼 둔다.
    - compose 주석의 "전용 CDP 망 기각"은 code-docker가 새 망에 들어가야 해서였다. 이번
      안은 브리지가 internal에 들어오는 쪽이라 그 이유와 충돌하지 않는다.
  - **비용**: Chrome이 code-docker/dind 안의 dev 서버(`http://code-docker:3000` 등)에
    직접 못 닿는다. router App Routes나 Dev Proxy 주소를 거쳐야 한다. 이 쓰임이 잦다면
    dev 서버용 App Route를 쉽게 만드는 길을 같이 설계할 것.
  - 미검증:
    - 바꾼 뒤 chrome-vnc/router 경로가 그대로 되는지
    - 브리지 별칭(`chrome-cdp`) 해석
    - `Browser.grantPermissions`가 LNA 권한을 실제로 주는지

## 4. 확장(extension) 개발 기반

이 레포의 존재 이유 중 하나가 **Chrome 확장을 개발할 수 있게 하는 것**인데, 그 바탕이
전혀 없다. 정할 것:

- 개발 중인 unpacked 확장을 어디서 로드하나 — code-docker의 `/code` 아래 프로젝트
  폴더를 Chrome 컨테이너에 읽기 전용으로 마운트 + `--load-extension`? 아니면 CDP의
  `Extensions.loadUnpacked`(chrome-devtools-mcp로 에이전트가 직접)?
- 리로드 루프: 에이전트가 코드 수정 → 확장 리로드 → 결과 확인까지를 CDP만으로 닫을 수
  있는지.
- 프로필 분리: 일상 브라우징 프로필(로그인 유지)과 확장 테스트용 깨끗한 프로필을
  나눌지.
- 3번 망 분리와 같이 설계할 것 — 마운트 경로와 브리지 위치가 서로 얽힌다.

- **결정 근거 정리 (2026-10-04 조사, 권장)**
  - 권장안:
    - 개발 중인 확장 폴더 **하나만** 읽기 전용으로 마운트한다(`/code` 전체는 금지). 두
      방식 모두 브라우저 쪽 경로에서 읽으므로 어느 쪽이든 필요하다.
    - 평소 루프는 CDP `Extensions.loadUnpacked`로 돌린다. chrome-devtools-mcp의
      install/reload 도구도 쓸 수 있다.
    - 안 되면 `CHROME_EXTRA_ARGS`로 `--load-extension`을 쓴다.
    - 확장 테스트는 **깨끗한 두 번째 프로필**(별도 볼륨)에서 한다.
  - 근거:
    - CDP 방식은 브라우저 재시작이 필요 없어 VNC 세션이 유지되고, 에이전트가 기존 CDP
      브리지만으로 수정-리로드-확인 루프를 닫을 수 있다.
    - `--load-extension`은 Chrome 정식판에서는 137에 제거됐지만, Chromium과 Chrome for
      Testing에서는 유지된다. 이 컨테이너는 Arch `chromium`이다.
      - 출처: groups.google.com/a/chromium.org/g/chromium-extensions/c/1-g8EFx2BBY
    - Puppeteer #14536 / PR #15059(2026-05, m149)가 `loadUnpacked`의 pipe 전용 제한을
      풀었다. 포트(WebSocket) 연결로도 될 가능성이 높다.
      - 출처: github.com/puppeteer/puppeteer/pull/15059, pptr.dev/guides/chrome-extensions
    - 프로필 분리:
      - 권한이 넓은 개발 확장이 일상 프로필의 로그인 세션을 읽을 수 있다.
      - `loadUnpacked`한 확장은 프로필(Secure Preferences)에 남는다.
      - Puppeteer와 Playwright도 테스트마다 새 프로필을 쓴다. 다만 일상 프로필과 나누라고
        명시한 출처는 없어서 추론이다.
  - **착수 전 실측 1회**: 컨테이너의 Chromium 151에서 `unwrap`을 거쳐 `Extensions.loadUnpacked`가
    되는지 확인한다. `--enable-unsafe-extension-debugging` 유무 두 경우를 다 본다.
  - 3번(망 분리)과는 독립적이다. 마운트는 Chrome 컨테이너에 붙으므로 망 구성과 무관하다.
