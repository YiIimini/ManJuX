#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""装配引擎 —— 拼接与接缝转场**交给 MoviePy（成熟引擎）**，平台不再自写滤镜图。

【为什么换掉自写那条链（2026-10-09 真机 P0）】
  平台原来用一条 `-filter_complex` 把 N 个镜头串成 xfade 链。真机《被开除一年后…第01集》
  因此合出**画面 23.708 秒 / 569 帧**（只有前 3 镜）而**音轨 122.499 秒** —— 第 4 镜起画面
  一直定格、声音照常走，而日志报「成片就绪 122 秒」。根因是 ffmpeg 的 xfade 在"过渡窗口
  末端越过前一镜真实末帧"时**静默丢弃后面所有输入、退出码仍是 0**；而"偏移按秒累加"与
  "xfade 每步输出落到整数帧"之间的误差，在一个 15 镜的成片里必然攒出来。
  「自己算偏移、自己拼图」正是成熟引擎每天在做的事 —— 交给它，平台只负责喂输入与核对结果。

【分工】
  ① 本脚本（MoviePy 15k★ / MIT / 活跃）：逐镜拼接 + 接缝转场（换场叠化；同场 1 帧=硬切）；
  ② 硬切（所有接缝 0 重叠）走 **ffmpeg concat 解复用器 + `-c copy`**：零重编码（成熟工具，
     没有任何时间算术，因此不可能重演上面那种越界）。
  ③ 拼接完成后的时长自检在**本脚本内**完成：实测画面比标称短 0.5 秒以上就 `ASM-FAIL` 拒交付。

【平台为什么不再自己算时间轴】
  引擎拼完的**真实**每镜起点由本脚本回报（`timeline` JSON）。平台的字幕落点、音轨窗口、
  母版时长一律读这一份 —— 一处算、处处一致；自写那套"按 fades 累加推起点"从此消失。

用法（平台侧；也可手工跑）：

  python assemble_mpy.py --manifest <manifest.json>

    manifest.json（键；路径一律绝对路径）：
      clips    [str]  必填，有序镜头文件
      fades    [num]  长度 = len(clips)-1，逐接缝重叠秒数；0 = 硬切
      fps      num    默认 24
      out      str    必填，输出母版（扩展名定容器；平台传 .mkv）
      timeline str    可选，回报真实时间轴的 JSON 路径
      audio_codec str 可选，默认 flac（中间轨无损：后面还要做响度归一）
      cut_copy bool   可选，默认 true：全硬切且各镜规格一致时走 concat 复制
      color_args [str] 可选：写进中间轨的色彩标记（-colorspace bt709 之类），逐字透传 ffmpeg

    stdout：进度行前缀 `ASM `；末行 `ASM-DONE video=<秒> frames=<n> expect=<秒> out=<路径>`；
            失败时 `ASM-FAIL <原因>` 且退出码非 0。

    timeline JSON：{"video_duration":..,"expected":..,"fps":..,"clips":[{"path":..,"start":..,"duration":..}]}

  手工一次性合片（可选，方便人眼复核）：

  python assemble_mpy.py --clips-dir <目录> --out <成片.mp4> [--shots 1,2,5]
      [--fades 0.5,0.0417] [--default-fade 0.5] [--audio <音轨>] [--ass a.ass] [--scale 1080:1920]
"""

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import time

sys.stdout.reconfigure(encoding="utf-8")

EXIT_FAIL = 3
VIDEO_KEYS = ("codec_name", "width", "height", "pix_fmt", "r_frame_rate")


def say(msg):
    print("ASM %s" % msg, flush=True)


def fail(msg):
    print("ASM-FAIL %s" % msg, flush=True)
    return EXIT_FAIL


def sh(cmd, cwd=None):
    """跑一条命令；失败时把 stderr 尾部带回来（不许静默）。"""
    p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True,
                       encoding="utf-8", errors="replace")
    if p.returncode != 0:
        tail = (p.stderr or "").strip().splitlines()[-8:]
        raise RuntimeError("命令失败(%s): %s\n%s" % (p.returncode, " ".join(cmd), "\n".join(tail)))
    return p.stdout or ""


def probe(path, stream=None):
    """取媒体信息。stream=None 取容器；'v'/'a' 取该流。返回 dict（取不到给 0/空）。"""
    cmd = ["ffprobe", "-v", "error", "-print_format", "json"]
    if stream:
        cmd += ["-select_streams", "%s:0" % stream,
                "-show_entries", "stream=duration,nb_frames,avg_frame_rate,r_frame_rate,"
                                 "width,height,pix_fmt,codec_name"]
    else:
        cmd += ["-show_entries", "format=duration"]
    try:
        j = json.loads(sh(cmd + [path]))
    except Exception:
        return {}
    if not stream:
        try:
            return {"duration": float(j["format"]["duration"])}
        except Exception:
            return {"duration": 0.0}
    try:
        st = j["streams"][0]
    except Exception:
        return {}
    d = st.get("duration")
    try:
        d = float(d) if d not in (None, "N/A") else 0.0
    except Exception:
        d = 0.0
    if d <= 0:
        # 没有 duration 时用帧数/帧率折算
        rate = st.get("avg_frame_rate") or st.get("r_frame_rate") or "0/1"
        try:
            num, den = rate.split("/")
            fps = float(num) / float(den)
        except Exception:
            fps = 0.0
        try:
            d = float(st.get("nb_frames") or 0) / fps if fps > 0 else 0.0
        except Exception:
            d = 0.0
    st["duration"] = d
    return st


def seconds(path, stream=None):
    return float(probe(path, stream).get("duration") or 0.0)


def video_seconds(path):
    """视频轨的**真实**时长（秒）。

    为什么不能只看 stream.duration：matroska 里它是 N/A（我们自己的中间轨就是 flac/mkv），
    实测上一版因此把 30 秒的画面报成 0 秒、音轨报成 0.000 —— 自检形同虚设。
    兜底顺序：流时长 → 帧数/帧率 → **最后一张视频包的 pts+dur**（只读索引，不解码）。
    """
    st = probe(path, "v")
    d = float(st.get("duration") or 0)
    if d > 0:
        return d
    rate = st.get("avg_frame_rate") or st.get("r_frame_rate") or "0/1"
    try:
        num, den = rate.split("/")
        fps = float(num) / float(den)
    except Exception:
        fps = 0.0
    nf = st.get("nb_frames")
    if nf and fps > 0:
        return float(nf) / fps
    try:
        out = sh(["ffprobe", "-v", "error", "-select_streams", "v:0",
                  "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", path])
    except Exception:
        return 0.0
    last = 0.0
    for line in out.splitlines():
        parts = line.strip().split(",")
        if not parts or not parts[0]:
            continue
        try:
            pts = float(parts[0])
            dur = float(parts[1]) if len(parts) > 1 and parts[1] else 0.0
        except ValueError:
            continue
        if pts + dur > last:
            last = pts + dur
    return last


def audio_seconds(path):
    """音轨时长（mkv 里同样是 N/A 的常客）⇒ 退到容器时长（= 最长的那条流）。"""
    d = float(probe(path, "a").get("duration") or 0)
    if d > 0:
        return d
    return float(probe(path).get("duration") or 0.0)


COLOR_KEYS = ("color_space", "color_primaries", "color_transfer", "color_range")


def color_of(path):
    """取该文件画面流上的四个色彩标记（取不到给 unknown）。"""
    try:
        out = sh(["ffprobe", "-v", "error", "-select_streams", "v:0",
                  "-show_entries", "stream=color_space,color_primaries,color_transfer,color_range",
                  "-of", "default=nw=1", path])
    except Exception:
        return {}
    got = {}
    for line in out.splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            got[k.strip()] = v.strip()
    return got


def apply_color_args(path, color_args):
    """把色彩标记**按复制流**写进成品（这是唯一写得进去的一条路）。

    【为什么不在编码时写】实测两条编码器（系统 ffmpeg 9.0.1 与引擎自带的 7.1）都**不认**
    `-colorspace / -color_primaries / -color_trc / -color_range` —— 编码器会把**输入帧自带**的
    色彩属性写出去（H3 源片是 bt709 / sRGB），显式选项被它盖掉；连 `-x264-params
    colorprim=...:transfer=...` 也一样被盖。而 `-c copy` 的重新封装**认这四个选项**
    （真机实测：color_transfer 从 iec61966-2-1 改成 bt709）。

    写不进去时**如实说**（不许让人以为标记已经在了）——探测出来是什么就报什么。"""
    if not color_args:
        return
    want = [str(x) for x in color_args]
    tagged = path + ".tagged.mkv"
    sh(["ffmpeg", "-y", "-v", "error", "-i", path, "-c", "copy"] + want + [tagged])
    got = color_of(tagged)
    say("色彩标记已按复制流写入（编码器不认这四个选项，实测只有复制这条路写得进去）")
    say("  成品标记 %s" % " ".join("%s=%s" % (k, got.get(k, "unknown")) for k in COLOR_KEYS))
    os.replace(tagged, path)


def subclip_to(c, d):
    """把片段裁到 d 秒（MoviePy 2.x 是 subclipped，1.x 是 subclip）。"""
    for name in ("subclipped", "subclip"):
        fn = getattr(c, name, None)
        if fn is not None:
            return fn(0, d)
    return c


def same_video_spec(paths):
    """各镜的视频规格是否完全一致（一致才允许走 concat 复制）。返回 (bool, 说明)。"""
    first = None
    for p in paths:
        st = probe(p, "v")
        key = tuple(str(st.get(k)) for k in VIDEO_KEYS)
        if first is None:
            first = (p, key)
            continue
        if key != first[1]:
            return False, ("第 1 镜 %s 与 %s 视频规格不一致（%s vs %s）"
                           % (os.path.basename(first[0]), os.path.basename(p),
                              first[1], key))
    return True, ""


def write_timeline(path, clips, video_duration, expected, fps):
    if not path:
        return
    doc = {"video_duration": round(video_duration, 6), "expected": round(expected, 6),
           "fps": fps, "clips": clips}
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(doc, fh, ensure_ascii=False, indent=1)


# ---------- ① 硬切：ffmpeg concat 解复用器 + 流复制（零重编码） ----------

def stitch_copy(paths, fades, out, fps, timeline):
    ok, why = same_video_spec(paths)
    if not ok:
        say("硬切但规格不一致 ⇒ 不走复制：%s" % why)
        return None
    # 有镜头完全没有音轨时不走复制：concat 复制会把那几镜的声音**整个丢掉**（静默丢内容）
    no_audio = [os.path.basename(p) for p in paths if not probe(p, "a")]
    if no_audio:
        say("硬切但有镜头没有音轨（%s）⇒ 不走复制（复制会把这几镜的声音整个丢掉）"
            % ", ".join(no_audio))
        return None
    tmpdir = tempfile.mkdtemp(prefix="asm-")
    lst = os.path.join(tmpdir, "concat.txt")
    with open(lst, "w", encoding="utf-8") as fh:
        for p in paths:
            fh.write("file '%s'\n" % p.replace("\\", "/").replace("'", "'\\''"))
    # 视频零重编码（流复制）；音轨转无损 flac —— 中间轨一律无损（后面响度归一/终混还要各编一次）
    sh(["ffmpeg", "-y", "-v", "error", "-f", "concat", "-safe", "0", "-i", lst,
        "-c:v", "copy", "-c:a", "flac", "-fflags", "+genpts", out])
    durs = [video_seconds(p) or seconds(p) for p in paths]
    starts, acc = [], 0.0
    for d in durs:
        starts.append(round(acc, 6))
        acc += d
    clips = [{"path": p, "start": starts[i], "duration": round(durs[i], 6)}
             for i, p in enumerate(paths)]
    write_timeline(timeline, clips, acc, acc, fps)
    say("硬切走 concat 复制（零重编码），%d 镜合计 %.3f 秒" % (len(paths), acc))
    return out, acc, acc, clips


# ---------- ② 转场：MoviePy 拼接（引擎自报真实时间轴） ----------

def stitch_moviepy(paths, fades, out, fps, timeline, audio_codec):
    from moviepy import VideoFileClip, concatenate_videoclips
    from moviepy.video.fx import CrossFadeIn, CrossFadeOut
    try:
        from moviepy.audio.fx import AudioFadeIn, AudioFadeOut
    except Exception:  # 老版本命名
        from moviepy.audio.fx.all import AudioFadeIn, AudioFadeOut

    n = len(paths)
    clips = [VideoFileClip(p) for p in paths]

    # 引擎说的"这一镜多长"与**视频轨实际帧数**可能不同（实测 shot_001：MoviePy 读到 9.900 秒
    # 而视频轨只有 9.833 秒 = 236 帧）⇒ 不裁齐的话每镜末尾会多出两帧**重复画面**
    # （正是用户最反感的"定格"形态）。按视频轨实际时长裁齐，时间轴也照实际回报。
    fixed = []
    for i, c in enumerate(clips):
        d = video_seconds(paths[i])
        if d > 0 and c.duration > d + 1e-6:
            say("第 %d 镜 引擎长度 %.3f 秒 → 视频轨实际 %.3f 秒：裁掉约 %d 帧（否则末尾是重复帧）"
                % (i + 1, c.duration, d, round((c.duration - d) * fps)))
            fixed.append(subclip_to(c, d))
        else:
            fixed.append(c)
    clips = fixed
    seq = []
    for i, c in enumerate(clips):
        cc = c
        if i > 0 and fades[i - 1] > 0:
            cc = cc.with_effects([CrossFadeIn(fades[i - 1])])
        if i < n - 1 and fades[i] > 0:
            cc = cc.with_effects([CrossFadeOut(fades[i])])
        # 音轨同样交叉淡入淡出：接缝处两镜的音频若直接叠加，会同时听见两份环境声/台词；
        # 一进一出地叠才是剪辑软件的做法（同场 1 帧时等同硬切，听不出来）。
        if cc.audio is not None:
            a = cc.audio
            if i > 0 and fades[i - 1] > 0:
                a = a.with_effects([AudioFadeIn(fades[i - 1])])
            if i < n - 1 and fades[i] > 0:
                a = a.with_effects([AudioFadeOut(fades[i])])
            cc = cc.with_audio(a)
        seq.append(cc)

    # 逐接缝拼接：起点取**引擎自己的**累计时长（不是平台推算的 fades 累加）
    acc = seq[0]
    starts = [0.0]
    durs = [float(seq[0].duration)]
    for i in range(1, n):
        f = fades[i - 1]
        starts.append(round(float(acc.duration) - f, 6))
        acc = concatenate_videoclips([acc, seq[i]], method="compose", padding=-f)
        durs.append(round(float(seq[i].duration), 6))
    total = float(acc.duration)
    expect = sum(durs) - sum(fades)
    say("拼接后 %.3f 秒（各镜合计 %.3f − 接缝重叠 %.3f = %.3f）"
        % (total, sum(durs), sum(fades), expect))

    tmpdir = tempfile.mkdtemp(prefix="asm-")
    work = os.path.join(tmpdir, "stitch" + os.path.splitext(out)[1])
    acc.write_videofile(work, fps=fps, codec="libx264", preset="veryfast",
                        audio_codec=audio_codec, audio_fps=48000,
                        ffmpeg_params=["-pix_fmt", "yuv420p"],
                        threads=max(2, (os.cpu_count() or 4) - 2), logger=None)
    # 引擎自己的产物搬回目标（同盘 mv；跨盘 copy）
    if os.path.exists(out):
        os.remove(out)
    os.replace(work, out)
    clips = [{"path": p, "start": starts[i], "duration": durs[i]} for i, p in enumerate(paths)]
    write_timeline(timeline, clips, total, expect, fps)
    for c in clips:
        c["path"] = os.path.basename(c["path"])
    return out, total, expect, clips


def run_stitch(paths, fades, out, fps, timeline, audio_codec, cut_copy, color_args):
    """硬切优先走 concat 复制（零重编码）；但**复制出来的时间轴要实测**：
    concat 解复用器按"文件时长"排下一段，而文件时长可能由**更长的音轨**决定
    （实测：画面 29.875 秒的三镜复制出来成了 30.046 秒 = 接缝处各多出 2 帧空隙，
    PLAYER 会停住那两帧 —— 正是我们要根除的"定格"）。多出来的空隙超过帧对齐余量就
    退回引擎重编，并如实说明：宁可多编一遍，也不留接缝处的停顿。

    色彩标记一律在最后按**复制流**写（见 apply_color_args）—— 编码那条路写不进去。"""
    n = len(paths)
    got = None
    if n > 1 and all(f <= 0 for f in fades) and cut_copy:
        got = stitch_copy(paths, fades, out, fps, timeline)
        if got:
            want = got[2]
            vd = video_seconds(out)
            slack = (n + 1) / float(fps)
            if vd > want + slack:
                say("硬切复制出来的画面 %.3f 秒 比各镜合计 %.3f 秒 多 %.3f 秒（超过 %d 帧余量）"
                    "⇒ 复制这条路的时间轴不对，改用引擎重编（宁可多编一遍，也不留接缝处的停顿）"
                    % (vd, want, vd - want, n + 1))
                got = None
    if got is None:
        got = stitch_moviepy(paths, fades, out, fps, timeline, audio_codec)
    apply_color_args(out, color_args)
    return got


def finish(out, want, clips, t0):
    """实测产物 + 自检。画面比标称短 0.5 秒以上 ⇒ 拒收（成熟引擎也会被喂错输入，
    这一步是平台的责任：宁可失败也不交付半部片子）。"""
    vd = video_seconds(out)
    ad = audio_seconds(out)
    if vd + 0.5 < want:
        return fail("成片画面只有 %.2f 秒，而标称 %.2f 秒（少 %.2f 秒）—— 拒绝交付"
                    % (vd, want, want - vd))
    nf = probe(out, "v").get("nb_frames")
    frames = int(float(nf)) if nf else int(round(vd * 24))
    say("产物 %s 画面=%.3f 秒 音轨=%.3f 秒 %d 帧" % (out, vd, ad, frames))
    print("ASM-DONE video=%.3f audio=%.3f frames=%d expect=%.3f out=%s elapsed=%.1fs"
          % (vd, ad, frames, want, out, time.time() - t0), flush=True)
    return 0


# ---------- 平台模式：manifest ----------

def main_manifest(args):
    t0 = time.time()
    with open(args.manifest, "r", encoding="utf-8") as fh:
        m = json.load(fh)
    paths = [str(p) for p in (m.get("clips") or [])]
    if not paths:
        return fail("manifest 里 clips 为空")
    missing = [p for p in paths if not os.path.isfile(p)]
    if missing:
        return fail("缺片段: %s" % ", ".join(os.path.basename(p) for p in missing))
    n = len(paths)
    fps = float(m.get("fps") or 24.0)
    fades = [float(x) for x in (m.get("fades") or [])]
    if len(fades) < n - 1:
        fades += [0.0] * (n - 1 - len(fades))
    fades = [max(0.0, f) for f in fades[: max(0, n - 1)]]
    out = str(m.get("out") or "")
    if not out:
        return fail("manifest 里 out 为空")
    timeline = str(m.get("timeline") or "")
    audio_codec = str(m.get("audio_codec") or "flac")
    cut_copy = bool(m.get("cut_copy", True))
    # 色彩标记逐字透传（平台量完全片口径才会给；空 = 谁都不许写，免得把"不统一"说成统一）
    color_args = [str(x) for x in (m.get("color_args") or [])]
    if color_args:
        say("色彩标记 %s" % " ".join(color_args))
    say("输入 %d 镜 fps=%g fades=%s" % (n, fps, [round(f, 4) for f in fades]))
    try:
        out, total, want, clips = run_stitch(paths, fades, out, fps, timeline, audio_codec,
                                            cut_copy, color_args)
    except Exception as e:
        return fail("拼接失败: %s" % e)
    return finish(out, want, clips, t0)


# ---------- 手工模式（人眼复核用；与平台同一条引擎） ----------

def main_manual(args):
    t0 = time.time()
    if not os.path.isdir(args.clips_dir):
        return fail("没有这个目录: %s" % args.clips_dir)
    if args.shots.strip():
        want = [int(x) for x in args.shots.replace("，", ",").split(",") if x.strip()]
        paths = [os.path.join(args.clips_dir, "shot_%03d.mp4" % i) for i in want]
        missing = [p for p in paths if not os.path.isfile(p)]
        if missing:
            return fail("缺片段: %s" % ", ".join(os.path.basename(p) for p in missing))
    else:
        paths = sorted((os.path.join(args.clips_dir, f) for f in os.listdir(args.clips_dir)
                        if re.fullmatch(r"shot_\d+\.mp4", f)),
                       key=lambda p: int(re.search(r"shot_(\d+)\.mp4", p).group(1)))
    if not paths:
        return fail("该目录里没有 shot_*.mp4")
    n = len(paths)
    if args.fades.strip():
        fades = [float(x) for x in args.fades.replace("，", ",").split(",") if x.strip()]
    else:
        fades = [args.default_fade] * max(0, n - 1)
    if len(fades) < n - 1:
        fades += [args.default_fade] * (n - 1 - len(fades))
    fades = [max(0.0, f) for f in fades[: max(0, n - 1)]]

    tmpdir = tempfile.mkdtemp(prefix="asm-")
    silent = os.path.join(tmpdir, "master.mkv")
    try:
        _, total, want, _ = run_stitch(paths, fades, silent, float(args.fps), "", "flac", False, [])
    except Exception as e:
        return fail("拼接失败: %s" % e)
    # 贴成品音轨 / 烧字幕 / 交付缩放：ffmpeg 标准做法（非脆弱结构）
    cmd = ["ffmpeg", "-y", "-v", "error", "-i", silent]
    if args.audio:
        cmd += ["-i", args.audio]
    vf = []
    if args.scale:
        w, h = args.scale.replace("x", ":").split(":")
        vf.append("scale=%s:%s:flags=lanczos" % (w, h))
    sub = args.ass or args.srt
    if sub:
        esc = sub.replace("\\", "/").replace(":", "\\:")
        vf.append("subtitles='%s'" % esc)
    if vf:
        cmd += ["-vf", ",".join(vf)]
    cmd += ["-map", "0:v:0"]
    if args.audio:
        cmd += ["-map", "1:a:0", "-c:a", "aac", "-b:a", "192k", "-shortest"]
    cmd += ["-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
            "-movflags", "+faststart", args.out]
    try:
        sh(cmd)
    except Exception as e:
        return fail("终混失败: %s" % e)
    return finish(args.out, want, None, t0)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", default="")
    ap.add_argument("--clips-dir", default="")
    ap.add_argument("--out", default="")
    ap.add_argument("--shots", default="")
    ap.add_argument("--fades", default="")
    ap.add_argument("--default-fade", type=float, default=0.5)
    ap.add_argument("--audio", default="")
    ap.add_argument("--ass", default="")
    ap.add_argument("--srt", default="")
    ap.add_argument("--scale", default="")
    ap.add_argument("--fps", type=float, default=24.0)
    args = ap.parse_args()
    if args.manifest:
        return main_manifest(args)
    if args.clips_dir and args.out:
        return main_manual(args)
    print("ASM-FAIL 要么给 --manifest，要么给 --clips-dir + --out", flush=True)
    return 2


if __name__ == "__main__":
    sys.exit(main())
