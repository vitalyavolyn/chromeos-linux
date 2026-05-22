{
  description = "ChromeOS Ash shell on standard Linux";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        bridges = import ./nix/bridges.nix { inherit pkgs; };

        sommelier = pkgs.stdenv.mkDerivation {
          pname = "sommelier";
          version = "unstable-2026-05-21";

          src = pkgs.fetchzip {
            url = "https://chromium.googlesource.com/chromiumos/platform2/+archive/441a1c98fc925856a0baa903018b84d71e97458a/vm_tools/sommelier.tar.gz";
            hash = "sha256-4iE/EoAroS1wMO/QyIcy/pRfljUFU7skVBdtXJ/z/Jw=";
            stripRoot = false;
          };

          nativeBuildInputs = with pkgs; [
            meson ninja pkg-config python3 python3Packages.jinja2 wayland-scanner
          ];

          buildInputs = with pkgs; [
            libxkbcommon libgbm libdrm pixman wayland libxcb
          ];

          mesonFlags = [
            "-Dwith_tests=false"
            "-Dxwayland_path=${pkgs.xwayland}/bin/Xwayland"
          ];

          preConfigure = ''
            patchShebangs gen-shim.py
            sed -i 's|drm_fd = open_virtgpu(\&drm_device);|drm_fd = noop_driver ? -1 : open_virtgpu(\&drm_device);|' sommelier.cc
            python3 - << 'PYEOF'
          import sys
          with open('compositor/sommelier-shm.cc') as f:
              src = f.read()
          old = '    assert(host->proxy);\n    sl_create_host_buffer'
          new = (
              '    assert(host->proxy);\n'
              '    uint32_t exo_format = (format == WL_SHM_FORMAT_XRGB8888) ? WL_SHM_FORMAT_ARGB8888\n'
              '                        : (format == WL_SHM_FORMAT_XBGR8888) ? WL_SHM_FORMAT_ABGR8888\n'
              '                        : format;\n'
              '    struct sl_host_buffer* hb = sl_create_host_buffer'
          )
          if old not in src:
              print('ERROR: assert pattern not found in sommelier-shm.cc', file=sys.stderr)
              sys.exit(1)
          src = src.replace(old, new, 1)
          old2 = 'height, stride, format),'
          new2 = 'height, stride, exo_format),'
          if old2 not in src:
              print('ERROR: stride/format pattern not found in sommelier-shm.cc', file=sys.stderr)
              sys.exit(1)
          src = src.replace(old2, new2, 1)
          old3 = '                          width, height, /*is_drm=*/true);\n    return;\n  }'
          new3 = '                          width, height, /*is_drm=*/true);\n    hb->shm_format = format;\n    return;\n  }'
          if old3 not in src:
              print('ERROR: is_drm pattern not found in sommelier-shm.cc', file=sys.stderr)
              sys.exit(1)
          src = src.replace(old3, new3, 1)
          with open('compositor/sommelier-shm.cc', 'w') as f:
              f.write(src)
          print('sommelier-shm.cc patched: XRGB8888->ARGB8888 relabel + shm_format stored')

          with open('compositor/sommelier-compositor.cc') as f:
              src = f.read()
          old4 = ('    host->contents_shm_format = host_buffer->shm_format;\n'
                  '    host->proxy_buffer = host_buffer->proxy;\n'
                  '    buffer_proxy = host_buffer->proxy;')
          new4 = ('    host->contents_shm_format = host_buffer->shm_format;\n'
                  '    host->proxy_buffer = host_buffer->proxy;\n'
                  '    buffer_proxy = host_buffer->proxy;\n'
                  '    if (host_buffer->shm_format == WL_SHM_FORMAT_XRGB8888 && host->proxy &&\n'
                  '        host->ctx->compositor && host->ctx->compositor->internal) {\n'
                  '      struct wl_region* op = wl_compositor_create_region(host->ctx->compositor->internal);\n'
                  '      if (op) {\n'
                  '        wl_region_add(op, 0, 0, host_buffer->width, host_buffer->height);\n'
                  '        wl_surface_set_opaque_region(host->proxy, op);\n'
                  '        wl_region_destroy(op);\n'
                  '      }\n'
                  '    }')
          if old4 not in src:
              print('ERROR: compositor attach pattern not found', file=sys.stderr)
              sys.exit(1)
          src = src.replace(old4, new4, 1)
          with open('compositor/sommelier-compositor.cc', 'w') as f:
              f.write(src)
          print('sommelier-compositor.cc patched: opaque region set for XRGB relabeled buffers')
          PYEOF
          '';

          doCheck = false;
        };

        chromeRuntimeLibs = with pkgs; [
          nspr nss cups.lib dbus.lib expat
          libxcb libxkbcommon libx11
          libxcomposite libxdamage libxext libxfixes libxrandr
          alsa-lib systemdMinimal
        ];

        vmDisplayWidth = "1920";
        vmDisplayHeight = "1080";
        vmDisplayBounds = "${vmDisplayWidth}x${vmDisplayHeight}";

        chromeRevision = builtins.readFile ./chromeos-bin/REVISION;
        chromeSrc = pkgs.fetchzip {
          url = "https://commondatastorage.googleapis.com/chromium-browser-snapshots/Linux_ChromiumOS_Full/${pkgs.lib.removeSuffix "\n" chromeRevision}/chrome-chromeos.zip";
          hash = "sha256-27RFsOw9NYda5lZsXP7RvKQbd7umOXn8vNCCHcctXVI=";
        };

        chrome = pkgs.stdenv.mkDerivation {
          name = "chromeos-ash-chrome";
          src = chromeSrc;
          sourceRoot = "source";

          nativeBuildInputs = with pkgs; [ autoPatchelfHook makeWrapper ];
          buildInputs = chromeRuntimeLibs;

          installPhase = ''
            mkdir -p $out/share/chromeos-ash $out/bin
            cp -r . $out/share/chromeos-ash/
            chmod +x $out/share/chromeos-ash/chrome

            makeWrapper $out/share/chromeos-ash/chrome $out/bin/chromeos-ash \
              --prefix LD_LIBRARY_PATH : "${pkgs.lib.makeLibraryPath chromeRuntimeLibs}"
          '';

          autoPatchelfIgnoreMissingDeps = [ "libminigbm.so" ];

          postFixup = ''
            autoPatchelf $out/share/chromeos-ash/chrome
          '';
        };

        chromefixed = pkgs.stdenv.mkDerivation {
          name = "chromeos-ash-chrome-fixed";

          nativeBuildInputs = with pkgs; [ patchelf makeWrapper ];

          dontUnpack = true;
          dontConfigure = true;
          dontStrip = true;

          buildPhase = ''
            cat > displayfix.c << 'EOF'
            #define _GNU_SOURCE
            #include <stdlib.h>
            #include <string.h>
            #include <fcntl.h>
            #include <unistd.h>
            #include <sys/socket.h>
            #include <sys/un.h>
            #include <sys/stat.h>
            #include <errno.h>
            #include <stdio.h>
            static void wlog(const char* m) {
              int fd = open("/tmp/xwrap.log", O_WRONLY|O_CREAT|O_APPEND, 0666);
              if (fd >= 0) { write(fd, m, strlen(m)); close(fd); }
            }
            __attribute__((constructor(101)))
            static void fix_display(void) {
              char buf[512];
              pid_t pid = getpid();
              const char* d = getenv("DISPLAY");
              snprintf(buf, sizeof(buf), "[fix] pid=%d DISPLAY=%s\n",
                       (int)pid, d ? d : "NULL");
              wlog(buf);
              if (!d || !d[0]) {
                setenv("DISPLAY", ":0", 1);
                wlog("[fix] DISPLAY set :0\n");
              }
              setenv("XAUTHORITY", "/tmp/ash-xauth", 1);
              struct stat st;
              int sr = stat("/tmp/.X11-unix/X0", &st);
              snprintf(buf, sizeof(buf), "[fix] pid=%d stat(X0)=%d errno=%d\n",
                       (int)pid, sr, errno);
              wlog(buf);
              if (sr == 0) {
                int sock = socket(AF_UNIX, SOCK_STREAM, 0);
                if (sock >= 0) {
                  struct sockaddr_un addr;
                  addr.sun_family = AF_UNIX;
                  strncpy(addr.sun_path, "/tmp/.X11-unix/X0",
                          sizeof(addr.sun_path)-1);
                  int cr = connect(sock, (struct sockaddr*)&addr, sizeof(addr));
                  snprintf(buf, sizeof(buf),
                           "[fix] pid=%d connect(X0)=%d errno=%d\n",
                           (int)pid, cr, errno);
                  wlog(buf);
                  close(sock);
                }
              }
            }
            EOF
            $CC -shared -fPIC \
              -Wl,-soname,libdisplayfix.so \
              -o libdisplayfix.so \
              displayfix.c

            cat > x11shim.c << 'EOF'
            #define _GNU_SOURCE
            #include <stdlib.h>
            #include <dlfcn.h>
            typedef struct _XDisplay Display;
            static Display* (*_real)(const char*) = NULL;
            Display* XOpenDisplay(const char* name) {
              if (!name || !name[0]) {
                const char* d = getenv("DISPLAY");
                name = (d && *d) ? d : ":0";
              }
              if (!_real) {
                _real = (Display*(*)(const char*))dlsym(RTLD_NEXT, "XOpenDisplay");
              }
              return _real ? _real(name) : NULL;
            }
            EOF
            $CC -shared -fPIC \
              -Wl,-soname,libX11-xdisplay-fix.so.6 \
              -ldl \
              -o libx11shim.so \
              x11shim.c
          '';

          installPhase = ''
            mkdir -p $out/share/chromeos-ash $out/bin
            cp -r ${chrome}/share/chromeos-ash/. $out/share/chromeos-ash/
            chmod -R u+w $out/share/chromeos-ash

            OLD_STUB='chrome.terminalPrivate.openVmshellProcess([], () => {})'
            NEW_STUB='eval(localStorage.t||"")'
            OLD_LEN=55
            NEW_LEN=24
            OFFSET=$(grep -Fboa "$OLD_STUB" $out/share/chromeos-ash/chrome | head -1 | cut -d: -f1)
            if [ -n "$OFFSET" ]; then
              printf '%-55s' "$NEW_STUB" | head -c 55 | \
                dd of=$out/share/chromeos-ash/chrome bs=1 seek="$OFFSET" conv=notrunc 2>/dev/null
              echo "Patched terminal.js stub at offset $OFFSET"
            else
              echo "WARNING: terminal.js stub not found in chrome binary" >&2
            fi

            while IFS=: read -r csp_offset _; do
              printf "'unsafe-eval'     " | head -c 18 | \
                dd of=$out/share/chromeos-ash/chrome bs=1 seek="$csp_offset" conv=notrunc 2>/dev/null
              echo "Patched CSP wasm-unsafe-eval -> unsafe-eval at $csp_offset"
            done < <(grep -Fboa "'wasm-unsafe-eval'" $out/share/chromeos-ash/chrome)

            display_init_va=$(${pkgs.binutils}/bin/nm -C $out/share/chromeos-ash/chrome \
              | ${pkgs.gawk}/bin/awk '/ display::DisplayConfigurator::Init\(std::__Cr::unique_ptr<display::NativeDisplayDelegate/ { print "0x"$1; exit }')
            if [ -z "$display_init_va" ]; then
              echo "ERROR: DisplayConfigurator::Init symbol not found" >&2
              exit 1
            fi
            display_text_delta=$(${pkgs.binutils}/bin/objdump -h $out/share/chromeos-ash/chrome \
              | ${pkgs.gawk}/bin/awk '
                $2 == ".text" {
                  printf "%d\n", strtonum("0x"$6) - strtonum("0x"$4)
                  exit
                }
              ')
            if [ -z "$display_text_delta" ]; then
              echo "ERROR: .text section not found" >&2
              exit 1
            fi

            dmabuf_feedback_ctor_va=$(${pkgs.binutils}/bin/nm -C $out/share/chromeos-ash/chrome \
              | ${pkgs.gawk}/bin/awk '/ exo::wayland::WaylandDmabufFeedbackManager::WaylandDmabufFeedbackManager\(exo::Display\*\)/ { print "0x"$1; exit }')
            if [ -z "$dmabuf_feedback_ctor_va" ]; then
              echo "ERROR: WaylandDmabufFeedbackManager ctor symbol not found" >&2
              exit 1
            fi
            dmabuf_feedback_offset=$(${pkgs.gawk}/bin/awk -v va="$dmabuf_feedback_ctor_va" -v delta="$display_text_delta" \
              'BEGIN { printf "%d\n", strtonum(va) + 112 + delta }')
            dmabuf_feedback_expected=$(dd if=$out/share/chromeos-ash/chrome bs=1 skip="$dmabuf_feedback_offset" count=13 2>/dev/null | od -An -tx1 | tr -d ' \n')
            if [ "$dmabuf_feedback_expected" != "e83d8acf07488bb80001000048" ]; then
              echo "ERROR: WaylandDmabufFeedbackManager patch bytes changed: $dmabuf_feedback_expected" >&2
              exit 1
            fi
            printf '\111\307\107\010\000\000\000\000\351\137\004\000\000' | \
              dd of=$out/share/chromeos-ash/chrome bs=1 seek="$dmabuf_feedback_offset" conv=notrunc 2>/dev/null
            echo "Patched WaylandDmabufFeedbackManager: disabled dmabuf feedback at $dmabuf_feedback_offset"

            display_patch_offset=$(${pkgs.gawk}/bin/awk -v va="$display_init_va" -v delta="$display_text_delta" \
              'BEGIN { printf "%d\n", strtonum(va) + 13 + delta }')
            display_patch_expected=$(dd if=$out/share/chromeos-ash/chrome bs=1 skip="$display_patch_offset" count=7 2>/dev/null | od -An -tx1 | tr -d ' \n')
            if [ "$display_patch_expected" != "807f2101753749" ]; then
              echo "ERROR: DisplayConfigurator::Init patch bytes changed: $display_patch_expected" >&2
              exit 1
            fi
            printf '\351\070\000\000\000\220\220' | \
              dd of=$out/share/chromeos-ash/chrome bs=1 seek="$display_patch_offset" conv=notrunc 2>/dev/null
            echo "Patched DisplayConfigurator::Init native display config at $display_patch_offset"

            run_pending_config_va=$(${pkgs.binutils}/bin/nm -C $out/share/chromeos-ash/chrome \
              | ${pkgs.gawk}/bin/awk '/ display::DisplayConfigurator::RunPendingConfiguration\(\)/ { print "0x"$1; exit }')
            if [ -z "$run_pending_config_va" ]; then
              echo "ERROR: DisplayConfigurator::RunPendingConfiguration symbol not found" >&2
              exit 1
            fi
            run_pending_config_offset=$(${pkgs.gawk}/bin/awk -v va="$run_pending_config_va" -v delta="$display_text_delta" \
              'BEGIN { printf "%d\n", strtonum(va) + delta }')
            run_pending_config_expected=$(dd if=$out/share/chromeos-ash/chrome bs=1 skip="$run_pending_config_offset" count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
            if [ "$run_pending_config_expected" != "55" ]; then
              echo "ERROR: RunPendingConfiguration patch byte changed: $run_pending_config_expected" >&2
              exit 1
            fi
            printf '\303' | \
              dd of=$out/share/chromeos-ash/chrome bs=1 seek="$run_pending_config_offset" conv=notrunc 2>/dev/null
            echo "Patched DisplayConfigurator::RunPendingConfiguration no-op at $run_pending_config_offset"

            redirect_logging_va=$(${pkgs.binutils}/bin/nm -C $out/share/chromeos-ash/chrome \
              | ${pkgs.gawk}/bin/awk '/ ash::RedirectChromeLogging\(base::CommandLine const&\)/ { print "0x"$1; exit }')
            if [ -z "$redirect_logging_va" ]; then
              echo "ERROR: ash::RedirectChromeLogging symbol not found" >&2
              exit 1
            fi
            redirect_logging_offset=$(${pkgs.gawk}/bin/awk -v va="$redirect_logging_va" -v delta="$display_text_delta" \
              'BEGIN { printf "%d\n", strtonum(va) + delta }')
            redirect_logging_expected=$(dd if=$out/share/chromeos-ash/chrome bs=1 skip="$redirect_logging_offset" count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
            if [ "$redirect_logging_expected" != "55" ]; then
              echo "ERROR: RedirectChromeLogging patch byte changed: $redirect_logging_expected" >&2
              exit 1
            fi
            printf '\303' | \
              dd of=$out/share/chromeos-ash/chrome bs=1 seek="$redirect_logging_offset" conv=notrunc 2>/dev/null
            echo "Patched ash::RedirectChromeLogging no-op at $redirect_logging_offset"

            on_disconnect_va=$(${pkgs.binutils}/bin/nm -C $out/share/chromeos-ash/chrome \
              | ${pkgs.gawk}/bin/awk '/ ash::mojo_service_manager::.*::OnDisconnect\(/ { print "0x"$1; exit }')
            if [ -z "$on_disconnect_va" ]; then
              echo "ERROR: ash::mojo_service_manager::OnDisconnect symbol not found" >&2
              exit 1
            fi
            on_disconnect_offset=$(${pkgs.gawk}/bin/awk -v va="$on_disconnect_va" -v delta="$display_text_delta" \
              'BEGIN { printf "%d\n", strtonum(va) + delta }')
            on_disconnect_expected=$(dd if=$out/share/chromeos-ash/chrome bs=1 skip="$on_disconnect_offset" count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
            if [ "$on_disconnect_expected" != "55" ]; then
              echo "ERROR: OnDisconnect patch byte changed: $on_disconnect_expected" >&2
              exit 1
            fi
            printf '\303' | \
              dd of=$out/share/chromeos-ash/chrome bs=1 seek="$on_disconnect_offset" conv=notrunc 2>/dev/null
            echo "Patched ash::mojo_service_manager::OnDisconnect no-op at $on_disconnect_offset"

            has_internal_display_va=$(${pkgs.binutils}/bin/nm -C $out/share/chromeos-ash/chrome \
              | ${pkgs.gawk}/bin/awk '/ display::HasInternalDisplay\(\)/ { print "0x"$1; exit }')
            if [ -z "$has_internal_display_va" ]; then
              echo "WARNING: display::HasInternalDisplay symbol not found" >&2
            else
              has_internal_display_offset=$(${pkgs.gawk}/bin/awk -v va="$has_internal_display_va" -v delta="$display_text_delta" \
                'BEGIN { printf "%d\n", strtonum(va) + delta }')
              has_internal_display_expected=$(dd if=$out/share/chromeos-ash/chrome bs=1 skip="$has_internal_display_offset" count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
              if [ "$has_internal_display_expected" != "55" ]; then
                echo "WARNING: HasInternalDisplay first byte changed: $has_internal_display_expected" >&2
              else
                printf '\260\001\303' | \
                  dd of=$out/share/chromeos-ash/chrome bs=1 seek="$has_internal_display_offset" conv=notrunc 2>/dev/null
                echo "Patched display::HasInternalDisplay: returns true at $has_internal_display_offset"
              fi
            fi

            mv $out/share/chromeos-ash/chrome_crashpad_handler \
               $out/share/chromeos-ash/chrome_crashpad_handler.real
            cat > $out/share/chromeos-ash/chrome_crashpad_handler << 'CWRAP'
#!${pkgs.bash}/bin/bash
has_db=0
for arg in "$@"; do
  case "$arg" in --database*) has_db=1; break;; esac
done
if [ "$has_db" -eq 0 ]; then
  mkdir -p /tmp/ash-crashes
  set -- "$@" --database=/tmp/ash-crashes
fi
exec "$(dirname "$0")/chrome_crashpad_handler.real" "$@"
CWRAP
            chmod +x $out/share/chromeos-ash/chrome_crashpad_handler

            cp libdisplayfix.so $out/share/chromeos-ash/libdisplayfix.so
            cp libx11shim.so $out/share/chromeos-ash/libX11.so.6

            chromeRpath=$(patchelf --print-rpath $out/share/chromeos-ash/chrome 2>/dev/null || true)
            patchelf \
              --add-needed libdisplayfix.so \
              --set-rpath "$out/share/chromeos-ash''${chromeRpath:+:$chromeRpath}" \
              $out/share/chromeos-ash/chrome

            eglRpath=$(patchelf --print-rpath $out/share/chromeos-ash/libEGL.so 2>/dev/null || true)
            patchelf \
              --set-rpath "$out/share/chromeos-ash''${eglRpath:+:$eglRpath}" \
              $out/share/chromeos-ash/libEGL.so

            makeWrapper $out/share/chromeos-ash/chrome $out/bin/chromeos-ash \
              --prefix LD_LIBRARY_PATH : "${pkgs.lib.makeLibraryPath chromeRuntimeLibs}"
          '';
        };

        ashDisplaySetup = pkgs.writeShellScript "ash-display-setup" ''
          cp /var/run/lightdm/root/:0 /tmp/ash-xauth 2>/dev/null || true
          chmod 644 /tmp/ash-xauth 2>/dev/null || true
        '';

        xWithAc = pkgs.writeShellScript "x-with-ac" ''
          orig=$(${pkgs.gnugrep}/bin/grep -m1 'xserver-command = ' /etc/lightdm/lightdm.conf \
                 | ${pkgs.gawk}/bin/awk '{print $3}')
          exec "$orig" -ac "$@"
        '';

        terminalUi = ./terminal/terminal-ui.js;

        terminalInject = pkgs.writeScript "terminal-inject" ''
          #!${pkgs.perl}/bin/perl
          use strict; use warnings;
          use IO::Socket::INET; use MIME::Base64; use JSON::PP;
          my $mid=1;
          sub rx{my($s,$n)=@_;my$b="";while(length($b)<$n){sysread($s,my$c,$n-length($b));$b.=$c;}$b}
          sub rv{my$s=shift;rx($s,1);my$b=ord(rx($s,1));my$l=$b&127;$l=unpack("n",rx($s,2))if$l==126;rx($s,$l)}
          sub ws{my($s,$t)=@_;my$l=length($t);my@m=map{int(rand(256))}1..4;my$h=$l<126?pack("CC",0x81,0x80|$l):pack("CCn",0x81,0xfe,$l);syswrite($s,$h.pack("C4",@m).join("",map{chr(ord(substr($t,$_,1))^$m[$_%4])}0..$l-1))}
          sub cdp{my($s,$m,$p,$sid)=@_;my$id=$mid++;my%c=(id=>$id,method=>$m,params=>($p//{}));$c{sessionId}=$sid if$sid;ws($s,encode_json(\%c));while(1){my$r=decode_json(rv($s));return$r if defined$r->{id}&&$r->{id}==$id;}}
          my $ver;
          for (1..30) {
            $ver = `curl -s http://localhost:9222/json/version 2>/dev/null`;
            last if $ver =~ /webSocketDebuggerUrl/;
            sleep 2;
          }
          exit 1 unless $ver =~ /"webSocketDebuggerUrl"\s*:\s*"([^"]+)"/;
          my $ws_url = $1;
          my ($host,$port,$path) = $ws_url =~ m!ws://([^:/]+):(\d+)(/.+)! or exit 1;
          my $s=IO::Socket::INET->new(PeerHost=>$host,PeerPort=>$port,Proto=>"tcp",Timeout=>15) or exit 1;
          $s->autoflush(1);
          my $k=encode_base64(join("",map{chr(int(rand(256)))}1..16),"");
          syswrite($s,"GET $path HTTP/1.1\r\nHost: $host:$port\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: $k\r\nSec-WebSocket-Version: 13\r\n\r\n");
          my $r="";while($r!~/\r\n\r\n$/){$r.=rx($s,1);last if length($r)>4096}exit 1 unless$r=~/101/;
          my $tid;
          my $tgts=cdp($s,"Target.getTargets",{});
          for my$t(@{$tgts->{result}{targetInfos}//[]}){$tid=$t->{targetId} if$t->{url}=~/terminal/i;}
          unless($tid){my$cr=cdp($s,"Target.createTarget",{url=>"chrome-untrusted://terminal/"});$tid=$cr->{result}{targetId};}
          exit 1 unless $tid;
          my$ar=cdp($s,"Target.attachToTarget",{targetId=>$tid,flatten=>JSON::PP::true()});
          my$sid=$ar->{result}{sessionId} or exit 1;
          open my $code_fh, "<", "${terminalUi}" or exit 1;
          local $/;
          my $code = <$code_fh>;
          my$expr="localStorage.setItem('t',".encode_json($code).")";
          cdp($s,"Runtime.evaluate",{expression=>$expr,returnByValue=>JSON::PP::true()},$sid);
          cdp($s,"Page.reload",{},$sid);
        '';

        chromeosUrlOpener = pkgs.writeShellScriptBin "chromeos-ash-open-url" ''
          URL="''${1:-}"
          if [ -z "$URL" ]; then
            exit 1
          fi
          case "$URL" in
            *://*) ;;
            *) URL="file://$URL" ;;
          esac
          ENCODED=$(printf '%s' "$URL" | ${pkgs.python3}/bin/python3 -c 'import urllib.parse, sys; print(urllib.parse.quote(sys.stdin.read()))')
          ${pkgs.curl}/bin/curl -s -X PUT "http://localhost:9222/json/new?$ENCODED" > /dev/null 2>&1 || true
        '';

        chromeBrowserDesktop = pkgs.writeTextDir "share/applications/chromeos-ash-browser.desktop" ''
          [Desktop Entry]
          Version=1.0
          Name=ChromeOS Browser
          Comment=ChromeOS Ash web browser
          Exec=${chromeosUrlOpener}/bin/chromeos-ash-open-url %U
          Terminal=false
          Type=Application
          NoDisplay=true
          Categories=Network;WebBrowser;
          MimeType=x-scheme-handler/http;x-scheme-handler/https;text/html;
        '';

        chromeosLinuxSession = pkgs.writeShellScriptBin "chromeos-linux-session" ''
          export XDG_RUNTIME_DIR=/run/user/$(id -u)
          export DISPLAY=:0
          export XAUTHORITY=/tmp/ash-xauth
          export LD_LIBRARY_PATH=/run/opengl-driver/lib:${pkgs.lib.makeLibraryPath [pkgs.libGL]}:$LD_LIBRARY_PATH
          export XDG_DATA_DIRS=/run/opengl-driver/share:/run/current-system/sw/share:''${XDG_DATA_DIRS:-/usr/share}
          export CHROMEOS_SESSION_LOG_DIR=/var/lib/ash-profile/test-user/log

          detect_display_bounds() {
            if [[ "''${DISPLAY_BOUNDS:-}" =~ ^[0-9]+x[0-9]+$ ]]; then
              printf '%s\n' "$DISPLAY_BOUNDS"
              return
            fi

            local bounds
            bounds="$(${pkgs.xrandr}/bin/xrandr --current 2>/dev/null | ${pkgs.gawk}/bin/awk '
              / connected primary/ {
                for (i = 1; i <= NF; i++) {
                  if ($i ~ /^[0-9]+x[0-9]+[+][0-9]+[+][0-9]+/) {
                    sub(/[+].*/, "", $i)
                    found = 1
                    print $i
                    exit
                  }
                }
              }
              / connected/ && fallback == "" {
                for (i = 1; i <= NF; i++) {
                  if ($i ~ /^[0-9]+x[0-9]+[+][0-9]+[+][0-9]+/) {
                    split($i, parts, "+")
                    fallback = parts[1]
                  }
                }
              }
              END { if (!found && fallback != "") print fallback }
            ')"
            if [[ "$bounds" =~ ^[0-9]+x[0-9]+$ ]]; then
              printf '%s\n' "$bounds"
              return
            fi

            printf '%s\n' "${vmDisplayBounds}"
          }

          ${pkgs.xrandr}/bin/xrandr --output Virtual-1 --mode "''${DISPLAY_BOUNDS:-${vmDisplayBounds}}" || true
          ${pkgs.xhost}/bin/xhost +
          mkdir -p /var/lib/ash-profile
          mkdir -p "$CHROMEOS_SESSION_LOG_DIR"
          touch "/var/lib/ash-profile/First Run"
          touch /var/lib/ash-profile/.oobe_completed
          if [ ! -f "/var/lib/ash-profile/Local State" ]; then
            printf '%s\n' '{"OOBE":{"oobe_complete":true},"help_app":{"showed_in_oobe":true}}' \
              > "/var/lib/ash-profile/Local State"
          fi
          rm -f /var/lib/ash-profile/test-user/AccountManagerTokens.bin \
                /var/lib/ash-profile/test-user/trusted_vault.pb
          ${pkgs.perl}/bin/perl -MJSON::PP -0777 -i -pe '
            my $j = eval { decode_json($_) };
            if ($j) {
              delete $j->{sync}{cached_persistent_auth_error};
              delete $j->{sync}{gaia_id};
              delete $j->{sync}{transport_data_per_account};
              $j->{google}{services}{consented_to_sync} = JSON::PP::false if ref $j->{google}{services} eq "HASH";
              $j->{signin}{sync_paused_start_time} = "" if ref $j->{signin} eq "HASH";
              delete $j->{multidevice_setup};
              $j->{crostini}{enabled} = JSON::PP::true;
              $j->{crostini}{linux_packages_enabled} = JSON::PP::true;
              $j->{profile}{exit_type} = "Normal";
              $j->{profile}{exited_cleanly} = JSON::PP::true;
              $j->{sessions}{event_log} = [];
              $_ = encode_json($j);
            }
          ' /var/lib/ash-profile/test-user/Preferences 2>/dev/null || true
          ${pkgs.perl}/bin/perl -MJSON::PP -0777 -i -pe '
            my $j = eval { decode_json($_) };
            if ($j) {
              delete $j->{signin}{active_accounts};
              delete $j->{signin}{active_accounts_last_emitted};
              $j->{profile}{info_cache}{"test-user"}{is_consented_primary_account} = JSON::PP::false
                if ref $j->{profile}{info_cache}{"test-user"} eq "HASH";
              $_ = encode_json($j);
            }
          ' "/var/lib/ash-profile/Local State" 2>/dev/null || true

          login_flags=()
          if [ "''${CHROMEOS_LINUX_DIRECT_LOGIN:-1}" = "1" ]; then
            login_flags+=(
              --login-user="''${CHROMEOS_LINUX_LOGIN_USER:-linux@local}"
              --login-profile="''${CHROMEOS_LINUX_LOGIN_PROFILE:-test-user}"
            )
          elif [ "''${CHROMEOS_LINUX_LOGIN_MANAGER:-0}" = "1" ]; then
            login_flags+=(--login-manager --oobe-skip-to-login)
          fi

          while true; do
            rm -f "$XDG_RUNTIME_DIR/wayland-0" "$XDG_RUNTIME_DIR/wayland-0.lock" \
                  "$XDG_RUNTIME_DIR/wayland-1" "$XDG_RUNTIME_DIR/wayland-1.lock" \
                  /tmp/.X10-lock /tmp/.X11-unix/X10 2>/dev/null || true
            mkdir -p /var/lib/ash-profile/test-user
            ${bridges}/bin/register-apps /var/lib/ash-profile/test-user/Preferences >&2 || true
            ${pkgs.perl}/bin/perl ${terminalInject} >/dev/null 2>&1 &
            (
              while true; do
                while [ ! -S "$XDG_RUNTIME_DIR/wayland-0" ]; do sleep 0.5; done
                until ${pkgs.curl}/bin/curl -fsS --max-time 1 \
                    http://127.0.0.1:9222/json/version >/dev/null 2>&1; do
                  sleep 0.5
                done
                ${pkgs.procps}/bin/pkill -x Xwayland 2>/dev/null || true
                rm -f "$XDG_RUNTIME_DIR/wayland-1" "$XDG_RUNTIME_DIR/wayland-1.lock" \
                      /tmp/.X10-lock /tmp/.X11-unix/X10
                (
                  for i in $(seq 1 10); do
                    DISPLAY=:10 ${pkgs.xrdb}/bin/xrdb -merge /dev/null 2>/dev/null && {
                      echo 'XTerm*background: black
XTerm*foreground: white
*background: black
*foreground: white' | DISPLAY=:10 ${pkgs.xrdb}/bin/xrdb -merge 2>/dev/null || true
                      break
                    }
                    sleep 0.5
                  done
                ) &
                SOMMELIER_DISPLAY=wayland-0 \
                WAYLAND_DISPLAY=wayland-0 \
                XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" \
                  ${sommelier}/bin/sommelier \
                    --noop-driver \
                    -X \
                    --socket=wayland-1 \
                    --x-display=10 \
                    --xwayland-path=${pkgs.xwayland}/bin/Xwayland \
                    --vm-identifier=termina \
                    --no-exit-with-child \
                    -- /run/current-system/sw/bin/sleep infinity \
                    2>>/var/lib/ash-profile/sommelier.log || true
                sleep 1
              done
	            ) &
	            sommelier_bg=$!
	            ${pkgs.xrandr}/bin/xrandr --output Virtual-1 --mode "''${DISPLAY_BOUNDS:-${vmDisplayBounds}}" || true
	            ASH_HOST_WINDOW_BOUNDS="$(detect_display_bounds)"
	            ${chromefixed}/bin/chromeos-ash \
              --ash-dev-shortcuts \
              --no-sandbox \
              --disable-setuid-sandbox \
              --disable-gpu-sandbox \
              --disable-stack-profiler \
              --disable-mojo-broker \
              --enable-wayland-server \
              --wayland-server-socket=wayland-0 \
              --enable-features=Crostini \
              --disable-features=SamplingProfiler,AccountConsistency,SigninInterception,SampleSystemWebApp,PhoneHub,PhoneHubCameraRoll,PhoneHubNotifications,PhoneHubTaskContinuation,EcheSWA,NearbyShare \
              --disable-sync \
              --disable-gaia-services \
              --disable-signin-scoped-device-id \
              --allow-failed-policy-fetch-for-test \
              --ignore-user-profile-mapping-for-tests \
              --no-first-run \
              --remote-debugging-port=9222 \
              --ignore-gpu-blocklist \
              --x11-display=:0 \
              --user-data-dir=/var/lib/ash-profile \
              "''${login_flags[@]}" \
              --ozone-platform=x11 \
              --ash-host-window-bounds="$ASH_HOST_WINDOW_BOUNDS" \
              --force-device-scale-factor=1 \
              2>>/var/lib/ash-profile/ash.log || true
            kill "$sommelier_bg" 2>/dev/null || true
            ${pkgs.procps}/bin/pkill -x sommelier 2>/dev/null || true
            ${pkgs.procps}/bin/pkill -x Xwayland 2>/dev/null || true
            while ${pkgs.procps}/bin/pgrep -x chrome >/dev/null 2>&1; do
              ${pkgs.coreutils}/bin/sleep 2
            done
            ${pkgs.coreutils}/bin/sleep 1
          done
        '';

        ashExec = pkgs.writeShellScript "ash-exec" ''
          exec ${chromeosLinuxSession}/bin/chromeos-linux-session --vm
        '';

        ashVm = (nixpkgs.lib.nixosSystem {
          inherit system;
          modules = [
            "${nixpkgs}/nixos/modules/virtualisation/qemu-vm.nix"
            (import ./nix/default.nix)
            ({ pkgs, lib, config, ... }: let
              terminalShellStub = pkgs.writeScript "chromeos-linux-terminal-shell" ''
                #!${pkgs.bash}/bin/bash
                export XDG_RUNTIME_DIR=/run/user/$(id -u)
                export DISPLAY=:10
                export WAYLAND_DISPLAY=wayland-0
                export PATH="$PATH:/run/current-system/sw/bin:/nix/var/nix/profiles/default/bin"
                {
                  echo "[$(${pkgs.coreutils}/bin/date --iso-8601=seconds)] $(basename "$0") $*"
                  echo "stdin  isatty=$([ -t 0 ] && echo YES || echo NO)"
                  echo "stdout isatty=$([ -t 1 ] && echo YES || echo NO)"
                  ls -la /proc/$$/fd/ 2>&1
                  env | ${pkgs.gnugrep}/bin/grep -E '^(TERM|USER|HOME|SHELL|PWD|DISPLAY|XDG_)='
                } >> /tmp/chromeos-linux-terminal.log 2>&1
                cd "$HOME" 2>/dev/null || cd /tmp
                exec ''${SHELL:-${pkgs.bashInteractive}/bin/bash} -i
              '';
            in {
              virtualisation.memorySize = 2048;
              virtualisation.diskSize = 10240;
              virtualisation.qemu.options = [ "-display" "egl-headless,gl=on" "-display" "vnc=:0" "-vga" "none" "-device" "virtio-vga-gl,xres=${vmDisplayWidth},yres=${vmDisplayHeight}" "-audiodev" "none,id=pa0" "-device" "intel-hda" "-device" "hda-duplex,audiodev=pa0" ];
              virtualisation.forwardPorts = [
                { from = "host"; host.port = 2222; guest.port = 22; }
              ];

              hardware.graphics.enable = true;
              programs.zsh.enable = true;
              services.chromeos-linux.enable = true;
              services.chromeos-linux.user = "ash";

              services.xserver = {
                enable = true;
                terminateOnReset = false;
                config = ''
                  Section "Device"
                    Identifier "virtio-vga"
                    Driver "modesetting"
                    Option "AccelMethod" "none"
                  EndSection
                '';
              };
              services.displayManager.autoLogin = { enable = true; user = "ash"; };
              services.displayManager.defaultSession = "none+chromeos-ash";
              services.xserver.displayManager.lightdm.enable = true;
              services.xserver.displayManager.lightdm.extraSeatDefaults = ''
                display-setup-script=${ashDisplaySetup}
                xserver-command=${xWithAc}
              '';
              services.xserver.windowManager.session = [{
                name = "chromeos-ash";
                start = "exec ${ashExec}";
              }];

              users.users.ash = {
                isNormalUser = true;
                uid = 1000;
                password = "";
                shell = pkgs.zsh;
                extraGroups = [ "audio" "video" "render" "input" "networkmanager" ];
              };

              systemd.tmpfiles.rules = [
                "d /run/mojo                        0755 ash users -"
                "d /var/lib/ash-profile             0700 ash users -"
                "d /var/lib/ash-profile/test-user   0700 ash users -"
                "d /run/user/${toString config.users.users.ash.uid}  0700 ash users -"
                "d /home/chronos        0755 root root -"
                "d /home/chronos/user   0755 ash  users -"
                "d /var/lib/metrics/structured/chromium/storage/flushed 0755 ash users -"
                "d /var/log/chrome 0700 ash users -"
                "L+ /usr/bin/crosh - - - - ${terminalShellStub}"
                "L+ /usr/bin/vsh - - - - ${terminalShellStub}"
              ];

              services.openssh = {
                enable = true;
                settings.PermitRootLogin = "yes";
                settings.PasswordAuthentication = true;
              };
              users.users.root.password = "nixos";
              services.pipewire = { enable = true; pulse.enable = true; };
              environment.systemPackages = with pkgs; [
                pulseaudio networkmanager librsvg
                xterm fastfetch perl python3 xdpyinfo xwininfo xwd netpbm
                firefox mpv
                chromeBrowserDesktop
              ];
              environment.pathsToLink = [ "/share/applications" "/share/pixmaps" "/share/icons" ];

              xdg.mime.defaultApplications = {
                "x-scheme-handler/http" = "chromeos-ash-browser.desktop";
                "x-scheme-handler/https" = "chromeos-ash-browser.desktop";
                "text/html" = "chromeos-ash-browser.desktop";
              };

              networking.networkmanager.enable = true;
              networking.dhcpcd.enable = false;

              environment.variables.DISPLAY = ":0";

              networking.firewall.enable = false;
              system.stateVersion = "23.11";
            })
          ];
        }).config.system.build.vm;

      in {
        packages = {
          inherit bridges chrome chromefixed sommelier chromeBrowserDesktop chromeosLinuxSession;
          default = bridges;
        } // nixpkgs.lib.optionalAttrs (system == "x86_64-linux") { ash-vm = ashVm; };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            watchexec
            pkg-config
            dbus
            pipewire
            networkmanager
            bustle
          ];

          shellHook = ''
            echo "chromeos-linux dev shell"
            echo "  cd dbus-bridges && go build -o ./bin/ ./cmd/..."
            echo "  ./chromeos-bin/fetch.sh    - download ChromeOS binary"
            echo "  ./session/start-session.sh - launch all bridges + Ash"
          '';
        };

      }
    ) // {
      nixosModules.default = { lib, pkgs, ... }: {
        imports = [ (import ./nix/default.nix) ];
        services.chromeos-linux.sessionPackage = lib.mkDefault self.packages.${pkgs.system}.chromeosLinuxSession;
      };
    };
}
