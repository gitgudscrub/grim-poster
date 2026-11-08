"""Generate a Windows amd64 .syso from a multi-resolution ICO before Go linking.

No post-link executable rewriting, third-party dependencies, network access, or secrets.
Compiles RT_ICON, RT_GROUP_ICON, and RT_VERSION into an AMD64 COFF object.
"""
from pathlib import Path
import argparse
import struct
S=struct.Struct
u16=lambda a,o:S('<H').unpack_from(a,o)[0]
def aligned4(b):
    b.extend(b'\x00'*((-len(b))%4))
    return b

def vs_block(key,value=b'',typ=1,children=(),value_length=None):
    out=bytearray(b'\x00'*6)
    out.extend(key.encode('utf-16le')+b'\x00\x00')
    aligned4(out)
    out.extend(value)
    aligned4(out)
    for child in children: out.extend(child)
    nval=(len(value)//2 if typ==1 else len(value)) if value_length is None else value_length
    S('<HHH').pack_into(out,0,len(out),nval,typ)
    return out

def make_version(version):
    components=[int(x) for x in version.split('.')]
    while len(components)<4: components.append(0)
    assert len(components)==4 and all(0<=x<=65535 for x in components)
    ver_hi=(components[0]<<16)|components[1]
    ver_lo=(components[2]<<16)|components[3]
    fixed=S('<13I').pack(0xFEEF04BD,0x00010000,ver_hi,ver_lo,ver_hi,ver_lo,
        0x3f,0,0x00040004,1,0,0,0)
    strings={
      'CompanyName':'Sharktower Community',
      'FileDescription':'Grim Poster Launcher for Blood on the Clocktower Online',
      'FileVersion':version,
      'InternalName':'GrimPoster',
      'OriginalFilename':'GrimPoster.exe',
      'ProductName':'Grim Poster',
      'ProductVersion':version,
      'Comments':'Unofficial community companion. Not affiliated with The Pandemonium Institute.'
    }
    string_children=[vs_block(k,(v+'\x00').encode('utf-16le')) for k,v in strings.items()]
    table=vs_block('040904B0',children=string_children,value_length=0)
    sfi=vs_block('StringFileInfo',children=(table,),value_length=0)
    var=vs_block('Translation',S('<HH').pack(0x409,0x4B0),typ=0)
    vfi=vs_block('VarFileInfo',children=(var,),value_length=0)
    return vs_block('VS_VERSION_INFO',fixed,typ=0,children=(sfi,vfi))

def make_resources(ico_path,version):
    p=ico_path.read_bytes()
    assert u16(p,0)==0 and u16(p,2)==1
    count=u16(p,4)
    assert 0<count<=32
    icons=[]
    for i in range(count):
        t=S('<BBBBHHII').unpack_from(p,6+i*16)
        w,h,colors,res,planes,bpp,size,offset=t
        assert offset+size<=len(p) and size>0
        icons.append((t,p[offset:offset+size]))
    group=bytearray(S('<HHH').pack(0,1,count))
    for i,(t,data) in enumerate(icons,1):
        width,height,colors,res,planes,bitcount,size,_=t
        group.extend(S('<BBBBHHIH').pack(width,height,colors,res,planes,bitcount,size,i))
    tree={3:{i:{1033:blob} for i,(_,blob) in enumerate(icons,1)},
          14:{1:{1033:bytes(group)}},
          16:{1:{1033:bytes(make_version(version))}}}
    out=bytearray();dirs=[];leaves=[];relocations=[]
    def allocate_dir(node):
        pos=len(out);out.extend(b'\0'*(16+8*len(node)));dirs.append((pos,node));return pos
    def allocate_entry(blob):
        pos=len(out);out.extend(b'\0'*16);leaves.append((pos,blob));return pos
    allocate_dir(tree)
    k=0
    while k<len(dirs):
        off,node=dirs[k];k+=1
        S('<IIHHHH').pack_into(out,off,0,0,0,0,0,len(node))
        for j,(id,value) in enumerate(sorted(node.items())):
            if isinstance(value,dict): offset=0x80000000|allocate_dir(value)
            else: offset=allocate_entry(value)
            S('<II').pack_into(out,off+16+j*8,id,offset)
    for off,blob in leaves:
        aligned4(out)
        ptr=len(out);out.extend(blob)
        S('<IIII').pack_into(out,off,ptr,len(blob),0,0)
        relocations.append(off)
    return out,relocations

def make_syso(ico_path,out_path,version):
    data,rels=make_resources(ico_path,version)
    # One .rsrc section and one static symbol; IMAGE_REL_AMD64_ADDR32NB
    # relocations make IMAGE_RESOURCE_DATA_ENTRY offsets point at linked RVAs.
    ptr_raw=20+40;ptr_reloc=ptr_raw+len(data);ptr_sym=ptr_reloc+10*len(rels)
    header=S('<HHIIIHH').pack(0x8664,1,0,ptr_sym,1,0,0)
    section=S('<8sIIIIIIHHI').pack(b'.rsrc\0\0\0',len(data),0,len(data),ptr_raw,ptr_reloc,0,len(rels),0,0x40000040)
    relocs=b''.join(S('<IIH').pack(o,0,3) for o in rels)
    symbol=S('<8sIhHBB').pack(b'.rsrc\0\0\0',0,1,0,3,0)
    out_path.write_bytes(header+section+data+relocs+symbol+S('<I').pack(4))
    print('Created',out_path,'size',out_path.stat().st_size,'relocations',len(rels))

if __name__=='__main__':
    a=argparse.ArgumentParser(description=__doc__)
    a.add_argument('--icon',type=Path,required=True)
    a.add_argument('--output',type=Path,required=True)
    a.add_argument('--version',required=True)
    x=a.parse_args();make_syso(x.icon,x.output,x.version)
