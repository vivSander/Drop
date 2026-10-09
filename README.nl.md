# Drop

Verstuur bestanden tussen je computer en je telefoon via je eigen wifi. Open Drop op beide apparaten, koppel ze één keer en sleep een bestand naar het andere apparaat: het komt meteen aan. Bestanden gaan rechtstreeks van het ene apparaat naar het andere: geen cloud, geen account, geen internet nodig, niets wordt ergens geüpload.

[English](README.md) · [Nederlands](README.nl.md)

## Downloaden

Pak het bestand voor jouw apparaat bij de **[nieuwste release](../../releases/latest)**.

| Je hebt | Download | Daarna |
|---|---|---|
| Windows | `Drop-Windows.zip` | Uitpakken, dubbelklik op `Drop.exe` |
| Mac (M1, M2, M3, M4…) | `Drop-Mac-AppleSilicon.zip` | Uitpakken, open `Drop.app` |
| Mac (ouder, Intel) | `Drop-Mac-Intel.zip` | Uitpakken, open `Drop.app` |
| Linux | `Drop-Linux-x86_64.tar.gz` of `Drop-Linux-arm64.tar.gz` | uitpakken, `./Drop` |
| Android-telefoon | `Drop.apk` | Openen en installeren |

Je hoeft niets in te stellen.

### Waarschuwing "onveilige app" / "onbekende uitgever"

Drop is gratis en open source. De downloads zijn **niet digitaal ondertekend**, want certificaten kosten elk jaar geld. Windows, macOS en Android waarschuwen daarom voor elke app van een nog onbekende ontwikkelaar. Dat zegt iets over de ontbrekende handtekening, niet over iets verdachts in het bestand. Zo weet je zeker dat je het echte bestand hebt:

1. Download alleen van de Releases-pagina van deze repository.
2. Vergelijk de checksum met `SHA256SUMS.txt` uit dezelfde release (`Get-FileHash Drop-Windows.zip` in Windows).
3. Optioneel: elke release heeft een ondertekend bewijs dat hij door GitHub Actions uit deze broncode is gebouwd: `gh attestation verify Drop-Windows.zip --repo <eigenaar>/<repo>`.
4. Of bouw het zelf, zie de Engelse README ("Build from source").

Daarna per systeem:

- **Windows:** "Windows heeft uw pc beveiligd" → **Meer informatie** → **Toch uitvoeren**. Sta in de firewall alleen **privénetwerken** toe.
- **Mac:** zegt het dat de app niet geopend kan worden, ga dan naar **Systeeminstellingen → Privacy en beveiliging**, scrol omlaag en kies **Open toch**.
- **Android:** sta installeren uit deze bron toe. Waarschuwt Play Protect, kies dan **Meer details → Toch installeren**. Sta meldingen toe, anders kan Android Drop niet laten draaien.

## Gebruiken

1. Open Drop op beide apparaten, op **hetzelfde wifi-netwerk**. Kies bij **Devices** welk netwerk je gebruikt.
2. Tik op het andere apparaat. Op één apparaat verschijnt een code van 6 cijfers; **typ die op het andere apparaat**. Dit doe je één keer per koppeling.
3. Tik opnieuw op het apparaat om zijn bestanden te zien. Sleep bestanden of mappen erop, of druk op **Send files**, en ze worden direct verstuurd.

- **Automatisch versturen:** alles wat je in je Drop-map zet, gaat binnen een paar seconden naar al je gekoppelde apparaten. Uitzetten kan bij **Devices**.
- **Waar het terechtkomt:** in een map met de naam van het verzendende apparaat, bijvoorbeeld `Pixel/foto.jpg`.
- **Apparaat verwijderen:** druk op het ✕ ernaast. Het heeft meteen geen toegang meer.
- **Afsluiten:** **Devices → Quit Drop** (computer) of **Stop** in de melding (Android).

## Is het veilig?

- Er is vanaf het netwerk niets bereikbaar behalve apparaten die jij hebt goedgekeurd. De pagina die je gebruikt draait alleen op je eigen computer.
- Alles tussen apparaten is versleuteld (TLS 1.3) en vastgepind aan het apparaat waarmee je koppelt, dus iemand op dezelfde wifi kan zich niet voordoen als je telefoon of meekijken.
- Koppelen vraagt om een code die jij zelf intypt. Een vreemde op je netwerk kan dus niets koppelen.
- Alleen je Drop-map wordt gedeeld, nooit de rest van je schijf, en alleen met gekoppelde apparaten.
- Geen account, geen tracking, geen verbinding met internet.

Let op: een gekoppeld apparaat kan alles in je Drop-map zien en er bestanden in zetten. Koppel alleen apparaten die van jou zijn of die je vertrouwt, en doe het koppelen op een netwerk dat je vertrouwt (thuis). Meer in [SECURITY.md](SECURITY.md).

## Problemen

- **Apparaten vinden elkaar niet:** ze moeten op hetzelfde netwerk zitten. Gast-wifi en sommige hotels blokkeren dit; gebruik de hotspot van je telefoon. Controleer of de firewall Drop op privénetwerken toestaat.
- **"Code komt niet overeen":** er is niets gekoppeld. Begin opnieuw en typ de nieuwe code zorgvuldig.

## Licentie

[MIT](LICENSE).
