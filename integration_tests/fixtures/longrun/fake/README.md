# Offline model attachment contract

Reader and Summariser implement model.ToolResultPartsInfo for PNG and PDF byte
uploads. model.NewClient and Reject preserve this capability for all three
registered offline model IDs. No live provider capability is invented.

Reader follows find with a text read at offset zero for asset locators. That
page lists attachment descriptors with the original 1-based part index. Reader
then requests part "attachment", index, offset zero, preserving the discovered
message and run. It uses only the bytes returned in ToolResultPart.Parts.
RequestText decodes those bytes through the same PNG/PDF decoder used for
original caller inputs. It never reads the scoring specification or asset files.

Multimodal scenarios allow 2,000 protected tool tokens because K22b charges 1,500
for an image. Tests require both assets to be absent at the need point, recovered
through recall, and used in the final action. Descriptor-only responses cannot
satisfy the evidence classifier. The workflow protocol remains 18.
