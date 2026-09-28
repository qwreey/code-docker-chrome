# 다음 작업 묶음 (백로그, 2026-09-28 작성)

한 번에 몰아서 할 작업 목록. 아직 착수 전이다. 사용자 메모: "VNC 바닥에 taskbar도 없고,
studio-docker 쪽 변경도 싱크가 안 된 상태라 나중에 한 번 작업해야 한다 — 테스트도
제대로 못 했다".

## 1. roblox-studio-docker에서 가져와야 할 변경 (싱크 누락)

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
