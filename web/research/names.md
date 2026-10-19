# VT-X kódová jména → předlohy

Data: `names.json` (stejná složka). Confidence:
- **official**: VOX předlohu jmenuje sám (jen VOX AC30/AC30TB) nebo jde o obecný typ (reverby ROOM/SPRING/HALL).
- **secondary**: značku uvádí sekundární zdroj a sedí s popisem v manuálu VOX.
- **inferred**: odhad z popisu VOX nebo jen z názvu. V UI zobrazovat jako „pravděpodobně …“.

Hlavní zjištění: VOX ve VT-X manuálech ani na webu značky předloh **nejmenuje**. Jen je opisuje („This models …“) a uvádí obecné upozornění na ochranné známky. Všechny značky cizích výrobců jsou proto secondary nebo inferred.

## Upozornění (verbatim, VOX)
> \* All product names and company names are the trademarks or registered trademarks of their respective owners.

VT100X OM s. 3. Stejný text je v manuálech VT20X/VT40X, VT+ a VTX150.

## Ampy (0x00–0x13)
| id | kód | předloha | conf. |
|---|---|---|---|
| 00/01 | DELUXE CL VIBRATO / NORMAL | Fender Deluxe Reverb ('65 blackface) | secondary |
| 02/03 | TWEED 4x10 BRIGHT / NORMAL | Fender Bassman 4x10 (1959) | secondary |
| 04/05 | BOUTIQUE CL / OD | Dumble Overdrive Special (VOX: „Overdrive Special“) | secondary |
| 06 | VOX AC30 | VOX AC30, kanál Normal | official |
| 07 | VOX AC30TB | VOX AC30 Top Boost (1964) | official |
| 08/09 | BRIT 1959 TREBLE / NORMAL | Marshall 1959 Super Lead 100W „Plexi“ | secondary |
| 0A | BRIT 800 | Marshall JCM800 (1983) | secondary |
| 0B | BRIT VM | Marshall JVM410H | secondary |
| 0C | SL-OD | Soldano SLO-100 | secondary |
| 0D | DOUBLE REC | Mesa/Boogie Dual Rectifier | secondary |
| 0E | CALI ELATION | Bogner Ecstasy, kanál 3 (jen jeden zdroj) | secondary |
| 0F/10 | ERUPT III CH2 / CH3 | EVH 5150III | secondary |
| 11 | BOUTIQUE METAL | Diezel VH4, kanál „Heavy“ | secondary |
| 12 | BRIT OR MKII | Orange Rockerverb 100 MkII | secondary |
| 13 | ORIGINAL CL | vlastní čistý zvuk VOX | secondary |

Rozpory: librarian uvádí BRIT OR MKII = „Orange Super Crush 100“. To je tranzistorový zesilovač bez lampového reverbu, takže nesedí s popisem VOX („two channels and a tube driven spring reverb“). ERUPT III librarian uvádí jako „Peavy 5150 III“, ale 5150III je značka EVH. Librarian má i překlepy „Marshal“ a „Peavy“.

## Pedal 1 (vše inferred)
COMP = obecný kompresor · CHORUS = analogový chorus typu Boss CE · TUBE OD = Ibanez Tube Screamer („green box“) · GOLD DRIVE = Klon Centaur („half-human half-horse“, VT+ OM) · TREBLE BOOST = VOX VBM-1 (VT+ OM) · RC TURBO = neznámo · ORANGE DIST = Boss DS-1 („Japanese-made … orange box“) · FAT DIST = neznámo · BRIT LEAD = neznámo · FUZZ = styl Fuzz Face.

## Pedal 2 (vše inferred)
FLANGER = MXR M-117 Flanger (EVH, „godfather of two-handed tapping“) · BLACK PHASER = neznámo · ORANGE PHASER 1/2 = MXR Phase 90 („orange box“) · TREMOLO = tremolo z Fender Twin („TWIN TREM“ ve VTX150) · TAPE ECHO = snad Roland RE-201 Space Echo (rychlost motoru) · ANALOG DELAY = BBD delay (např. Boss DM-2).

## Reverb
ROOM, SPRING, HALL: official, obecné typy bez předlohy. PLATE: studiový deskový reverb, odhad EMT 140 (inferred).

## Zdroje
- VT100X OM (lokálně): `(VOX manual text) VT100X_OM.txt` (ř. 137, 536–658)
- VT20X/VT40X OM: https://voxamps.com/wp-content/uploads/2019/12/VT20X_VT40X_OM_EFGSJ1_WEB.pdf
- Produktová stránka VOX VT100X (jen seznam 11/20 modelů, bez předloh): https://voxamps.com/en-gb/?p=13291
- VT+ OM (VOX, popisy GOLD DRIVE, TREBLE BOOST, FUZZ, BBD delay): https://www.voxshowroom.com/ct/manuals/VT+.pdf
- VTX150 Neodymium OM (VOX, stejné popisy): https://medias.audiofanzine.com/files/vtx150neodymium-om-e2-479210.pdf
- Cambridge50 OM („orange-colored box“): https://voxamps.com/wp-content/uploads/2020/01/VOX_Cambridge50_OM_EFGSJ1.pdf (popis převzatý z výsledku vyhledávání, PDF neotevřeno)
- vox-amp-librarian `amps.kt` @c979950: https://github.com/tmarsteel/vox-amp-librarian/blob/c9799506ca2a9af934dd81d4eddd49892618e167/src/main/kotlin/com/github/tmarsteel/voxamplibrarian/appmodel/amps.kt (lokálně `(vox-amp-librarian source) `)
- ToneBucket, Amp model guide: https://tonebucket.com/resources (fanouškovský zdroj, jediný, který uvádí CALI ELATION a ERUPT III; ověřeno v raw HTML, uloženo `(tonebucket.com/resources snapshot)`)
- VTX MIDI Implementation (kódová jména RC TURBO, FAT DIST, BRIT LEAD, BLACK/ORANGE 1/ORANGE 2 phaser): `(VOX manual text) VTX_Midi_Impl.txt` ř. 519–612
- Audiofanzine, test VT20X (potvrzuje seznam pedálů v Tone Room): https://fr.audiofanzine.com/ampli-combo-hybride-pour-guitare/vox/vt20x-classic-blue/editorial/tests/test-de-l-ampli-pour-guitare-electrique-vox-vt20x-classic-blue.html

Nenalezeno: oficiální nápověda Tone Room s popisy 9 modelů navíc a jakýkoli popis RC TURBO, FAT DIST, BRIT LEAD a BLACK PHASER.
