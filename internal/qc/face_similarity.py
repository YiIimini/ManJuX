#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""人脸一致性测量（insightface buffalo_l，512 维嵌入 + 余弦相似度）。

用法：
    python face_similarity.py --ref <定妆照> --frames <帧1> <帧2> ... [--json out.json]

输出（stdout，纯 JSON，一行）：
    {"ok": true, "ref_faces": 1, "items": [{"frame": "...", "faces": 1, "sim": 0.9123}, ...],
     "min": 0.91, "mean": 0.93, "below_threshold": 0, "threshold": 0.85}

【为什么是"测量"而不是"判定"】阈值 0.85 来自开源参考实现的实测（YYC3 的 G3 预研记录），
但在**本平台的动画/3D 写实画风**上还没有跨镜标定数据 ⇒ 这一版只如实报告相似度，
由平台侧先记录、后定阈值（判据不成熟就拦人，只会制造假告警，反而让人不再看告警）。

【两个必须处理的坑】
  1. Windows 上 cv2.imread 读不了**非 ASCII 路径**（本项目全是中文目录）⇒ 一律用
     np.fromfile + cv2.imdecode。
  2. 画面里可能有多张脸 ⇒ 取**面积最大**的那张（主角通常占画面更大），并把脸数一并报出来
     （脸数本身是有用的信号：该有脸却没有脸 = 检测失败，不能当成"相似度 0"）。

退出码：0=测量完成（哪怕相似度低）；2=环境或输入有问题（缺模型/读不到图），
由平台侧记为"没验过"而不是"通过"。
"""
import argparse
import json
import os
import sys


def _fail(msg):
    print(json.dumps({"ok": False, "error": msg}, ensure_ascii=False))
    sys.exit(2)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--ref", required=True)
    ap.add_argument("--frames", nargs="+", required=True)
    ap.add_argument("--model-root", default=os.environ.get("MANJUX_FACE_MODEL_ROOT", ""))
    ap.add_argument("--threshold", type=float, default=0.85)
    args = ap.parse_args()

    try:
        import numpy as np
        import cv2
        from insightface.app import FaceAnalysis
    except Exception as e:  # 依赖缺失 ⇒ 明确报"没能力验"
        _fail("缺少依赖(insightface/onnxruntime/opencv): %s" % e)

    root = args.model_root or None
    if root:
        os.environ.setdefault("INSIGHTFACE_HOME", root)
    try:
        app = FaceAnalysis(name="buffalo_l", root=root, providers=["CPUExecutionProvider"])
        app.prepare(ctx_id=-1, det_size=(640, 640))
    except Exception as e:
        _fail("加载 buffalo_l 失败(模型没装或路径不对): %s" % e)

    def read_img(p):
        try:
            data = np.fromfile(p, dtype=np.uint8)
        except Exception:
            return None
        if data is None or data.size == 0:
            return None
        return cv2.imdecode(data, cv2.IMREAD_COLOR)

    def biggest(img):
        faces = app.get(img)
        if not faces:
            return None, 0
        faces = sorted(faces, key=lambda f: -(f.bbox[2] - f.bbox[0]) * (f.bbox[3] - f.bbox[1]))
        return faces[0], len(faces)

    ref_img = read_img(args.ref)
    if ref_img is None:
        _fail("读不到参考图: %s" % args.ref)
    ref_face, ref_n = biggest(ref_img)
    if ref_face is None:
        _fail("参考图里没检出人脸: %s" % args.ref)

    items = []
    sims = []
    for fr in args.frames:
        img = read_img(fr)
        if img is None:
            items.append({"frame": fr, "faces": 0, "sim": None, "note": "读不到图"})
            continue
        face, n = biggest(img)
        if face is None:
            items.append({"frame": fr, "faces": 0, "sim": None, "note": "没检出人脸"})
            continue
        s = float(np.dot(ref_face.normed_embedding, face.normed_embedding))
        sims.append(s)
        items.append({"frame": os.path.basename(fr), "faces": n, "sim": round(s, 4)})

    out = {
        "ok": True,
        "ref_faces": ref_n,
        "items": items,
        "threshold": args.threshold,
        "measured": len(sims),
        "below_threshold": sum(1 for s in sims if s < args.threshold),
    }
    if sims:
        out["min"] = round(min(sims), 4)
        out["mean"] = round(sum(sims) / len(sims), 4)
        out["max"] = round(max(sims), 4)
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main()
