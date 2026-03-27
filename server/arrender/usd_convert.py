#!/usr/bin/env python3
"""Extract mesh data from USD/USDA/USDC/USDZ files.

Outputs JSON with geometry, materials, and transforms to stdout.
Usage: python3 usd_convert.py <input_path>

Requires: pip install usd-core
"""
import json
import sys


def extract(input_path):
    """Open a USD stage and extract all mesh prims as JSON."""
    try:
        from pxr import Usd, UsdGeom, UsdShade, Gf  # noqa: F401
    except ImportError:
        return {"error": "pxr module not found; install usd-core: pip install usd-core"}

    stage = Usd.Stage.Open(input_path)
    if not stage:
        return {"error": "could not open " + input_path}

    result = {"meshes": []}

    for prim in stage.Traverse():
        if not prim.IsA(UsdGeom.Mesh):
            continue

        mesh = UsdGeom.Mesh(prim)

        points = mesh.GetPointsAttr().Get()
        if not points:
            continue

        indices = mesh.GetFaceVertexIndicesAttr().Get()
        counts = mesh.GetFaceVertexCountsAttr().Get()
        if not indices or not counts:
            continue

        normals_attr = mesh.GetNormalsAttr()
        normals = normals_attr.Get() if normals_attr.HasValue() else None

        # UVs: check primvars:st (common) and texCoord_0.
        uvs = None
        for uv_name in ["primvars:st", "primvars:st0", "primvars:texCoord_0"]:
            uv_attr = prim.GetAttribute(uv_name)
            if uv_attr and uv_attr.HasValue():
                uvs = uv_attr.Get()
                break

        # Compute local-to-world transform.
        xform = UsdGeom.Xformable(prim)
        transform = xform.ComputeLocalToWorldTransform(Usd.TimeCode.Default())

        # Extract material properties.
        material_data = _extract_material(prim)

        # Triangulate polygon soup (USD meshes can contain quads and n-gons).
        tri_indices = _triangulate(indices, counts)

        mesh_data = {
            "name": str(prim.GetPath()),
            "vertices": [[float(p[0]), float(p[1]), float(p[2])] for p in points],
            "indices": tri_indices,
            "normals": [[float(n[0]), float(n[1]), float(n[2])] for n in normals] if normals else [],
            "uvs": [[float(uv[0]), float(uv[1])] for uv in uvs] if uvs else [],
            "transform": [
                [float(transform[r][c]) for c in range(4)]
                for r in range(4)
            ],
            "material": material_data,
        }

        result["meshes"].append(mesh_data)

    return result


def _extract_material(prim):
    """Extract UsdPreviewSurface material parameters from a prim's bound material."""
    from pxr import UsdShade

    default = {"baseColor": [0.8, 0.8, 0.8], "metallic": 0.0, "roughness": 0.5}

    binding = UsdShade.MaterialBindingAPI(prim)
    bound = binding.ComputeBoundMaterial()
    if not bound or not bound[0]:
        return default

    mat = bound[0]

    # Try to find a UsdPreviewSurface shader.
    surface = None
    for source_type in ["mdl", ""]:
        result = mat.ComputeSurfaceSource(source_type) if source_type else mat.ComputeSurfaceSource()
        if result and result[0]:
            surface = result[0]
            break

    if not surface:
        return default

    result = dict(default)

    # Base color / diffuse color.
    for input_name in ["diffuseColor", "baseColor"]:
        inp = surface.GetInput(input_name)
        if not inp:
            continue
        val = inp.Get()
        if val is not None and hasattr(val, '__len__') and len(val) >= 3:
            result["baseColor"] = [float(val[0]), float(val[1]), float(val[2])]
            break

    # Metallic.
    inp = surface.GetInput("metallic")
    if inp:
        val = inp.Get()
        if val is not None:
            result["metallic"] = float(val)

    # Roughness.
    inp = surface.GetInput("roughness")
    if inp:
        val = inp.Get()
        if val is not None:
            result["roughness"] = float(val)

    return result


def _triangulate(indices, counts):
    """Convert polygon soup (quads, n-gons) to a flat triangle index list."""
    tri_indices = []
    offset = 0
    for count in counts:
        for i in range(count - 2):
            tri_indices.append(int(indices[offset]))
            tri_indices.append(int(indices[offset + i + 1]))
            tri_indices.append(int(indices[offset + i + 2]))
        offset += int(count)
    return tri_indices


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: usd_convert.py <input_path>", file=sys.stderr)
        sys.exit(1)

    data = extract(sys.argv[1])
    if "error" in data:
        print(data["error"], file=sys.stderr)
        sys.exit(1)

    json.dump(data, sys.stdout)
