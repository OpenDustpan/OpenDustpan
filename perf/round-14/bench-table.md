| benchmark | old ns/op | new ns/op | change | old allocs | new allocs | old B/op | new B/op |
|---|---|---|---|---|---|---|---|
| BenchmarkAuditAppend | only in new |||||||
| BenchmarkAuthorizer/check | 3400354 | 3260354 | -4.1% | 792 | 792 | 61415 | 61434 |
| BenchmarkAuthorizer/list_objects | 2580410 | 2437046 | -5.6% | 4840 | 4840 | 386378 | 386373 |
| BenchmarkCallerGrants/others=0 | 5930784 | 4930266 | -16.9% | 3146 | 3299 | 341359 | 376864 |
| BenchmarkCallerGrants/others=100000 | 4737368 | 5151126 | +8.7% | 3145 | 3298 | 341345 | 376848 |
| BenchmarkCallerGrants/others=20000 | 8341844 | 7850020 | -5.9% | 3145 | 3298 | 341365 | 376848 |
| BenchmarkCheckpointSaveDelta1000 | 452970 | 474184 | +4.7% | 326 | 326 | 13200 | 13203 |
| BenchmarkCheckpointSaveDelta3000 | 437248 | 438218 | +0.2% | 326 | 326 | 13190 | 13190 |
| BenchmarkCheckpointSaveFull1000 | 277251 | 261370 | -5.7% | 270 | 270 | 11000 | 11001 |
| BenchmarkCheckpointSaveFull3000 | 252272 | 242370 | -3.9% | 270 | 270 | 10998 | 10998 |
| BenchmarkCollectorChunks | 918371530 | 877082084 | -4.5% | 3397124 | 3397124 | 215005452 | 215006708 |
| BenchmarkCountManagedResources1000 | 9792666 | 9431219 | -3.7% | 51319 | 51319 | 3883258 | 3883260 |
| BenchmarkCountManagedResources3000 | 27451468 | 27645679 | +0.7% | 153879 | 153879 | 11693178 | 11693178 |
| BenchmarkDeltaApply1M | 783452 | 737343 | -5.9% | 14 | 14 | 1059441 | 1059502 |
| BenchmarkDeltaApply256K | 134654 | 110748 | -17.8% | 11 | 11 | 271714 | 271716 |
| BenchmarkDeploymentShape1M | 1383646 | 1575812 | +13.9% | 8038 | 8038 | 51872 | 51872 |
| BenchmarkDeploymentShapeSequenced1M | 1364525 | 1514618 | +11.0% | 8041 | 8041 | 51968 | 51968 |
| BenchmarkDeploymentShapeSequenced4M | 5469989 | 5844905 | +6.9% | 32091 | 32091 | 205888 | 205888 |
| BenchmarkFencedWrite/check/parallel8 | 84676 | 92807 | +9.6% | 135 | 135 | 6842 | 6842 |
| BenchmarkFencedWrite/check/serial | 376900 | 400021 | +6.1% | 132 | 132 | 6667 | 6667 |
| BenchmarkFencedWrite/fence_only/parallel8 | 68691 | 76817 | +11.8% | 88 | 88 | 5504 | 5504 |
| BenchmarkFencedWrite/fence_only/serial | 318316 | 325762 | +2.3% | 85 | 85 | 5332 | 5329 |
| BenchmarkGateAuthorize/inside-last | 450 | 458 | +1.9% | 0 | 0 | 0 | 0 |
| BenchmarkGateAuthorize/outside-all | 895 | 865 | -3.4% | 4 | 4 | 144 | 144 |
| BenchmarkGzipBestSpeedCompress | 615562 | 537800 | -12.6% | 21 | 21 | 821907 | 821906 |
| BenchmarkGzipBestSpeedDecompress | 99634 | 94364 | -5.3% | 8 | 8 | 41295 | 41300 |
| BenchmarkIPAllowlistAuthorize1000Stacks | 15489276 | 16317344 | +5.3% | 33013 | 33012 | 1153605 | 1153432 |
| BenchmarkIPAllowlistServeNamespaceRead | 62766 | 72182 | +15.0% | 161 | 161 | 13417 | 13418 |
| BenchmarkIPAllowlistServeNamespaceReadOff | 43273 | 51366 | +18.7% | 128 | 128 | 12263 | 12263 |
| BenchmarkJournalBuildChange10000 | 296452812 | 289883032 | -2.2% | 690000 | 690012 | 85542948 | 85544802 |
| BenchmarkJournalBuildChange10in10000 | 122300836 | 114488808 | -6.4% | 111097 | 111092 | 36206168 | 36203742 |
| BenchmarkJournalBuildChange3000 | 95700800 | 90605060 | -5.3% | 206765 | 206757 | 24209782 | 24208598 |
| BenchmarkJournalBuildCreate10000 | 269497342 | 256502004 | -4.8% | 629912 | 629910 | 79210523 | 79212117 |
| BenchmarkJournalBuildCreate3000 | 83235130 | 77971224 | -6.3% | 188715 | 188708 | 22351722 | 22349668 |
| BenchmarkJournalBuildSmallestState | 227279946 | 216646276 | -4.7% | 599834 | 599842 | 69884184 | 69887495 |
| BenchmarkLeaseCheck/postgres/with-check | 873008 | 890928 | +2.1% | 329 | 329 | 29560 | 29544 |
| BenchmarkLeaseCheck/postgres/without-check | 608479 | 616089 | +1.3% | 272 | 272 | 25096 | 25101 |
| BenchmarkLeaseCheck/sqlite/with-check | 547385 | 515192 | -5.9% | 619 | 619 | 31342 | 31335 |
| BenchmarkLeaseCheck/sqlite/without-check | 365522 | 412986 | +13.0% | 498 | 498 | 27268 | 27270 |
| BenchmarkPrincipalLists/groups | 1712586 | 1154046 | -32.6% | 952 | 952 | 66221 | 66220 |
| BenchmarkPrincipalLists/groups_of_a_principal_in_80_groups | 1658667 | 1268204 | -23.5% | 801 | 801 | 59768 | 59770 |
| BenchmarkPrincipalLists/members_of_a_group | 679516 | 412998 | -39.2% | 196 | 196 | 14352 | 14352 |
| BenchmarkPrincipalLists/principals | 2945354 | 1730196 | -41.3% | 1110 | 1110 | 101805 | 101808 |
| BenchmarkPrincipalLists/principals_with_a_prefix | 2554411 | 1677164 | -34.3% | 1114 | 1114 | 101870 | 101872 |
| BenchmarkPrincipalLists/services_with_a_prefix | 3115342 | 2364920 | -24.1% | 1106 | 1106 | 101223 | 101216 |
| BenchmarkPrincipalLists/workloads,_2_percent_of_the_principals | 3520246 | 3080060 | -12.5% | 1102 | 1102 | 101556 | 101556 |
| BenchmarkRecorderPutPlan | only in new |||||||
| BenchmarkRunnerClaim | only in new |||||||
| BenchmarkScanPointersManyPointersManyMembers | 104772528 | 120851482 | +15.3% | 501250 | 501250 | 46054584 | 46054602 |
| BenchmarkUpload32MiB | 346168484 | 343305224 | -0.8% | 226059 | 226066 | 6738738 | 6739572 |
| BenchmarkWriteCheck/state,_fence_and_minimum,_one_set | 387288 | 271472 | -29.9% | 116 | 116 | 5448 | 5436 |
| BenchmarkWriteCheck/state_and_minimum,_none_set | 135398 | 136650 | +0.9% | 63 | 63 | 2406 | 2406 |
| BenchmarkWriteCheck/state_and_minimum,_one_set | 150602 | 134338 | -10.8% | 66 | 66 | 2526 | 2526 |
| BenchmarkWriteCheck/state_only | 116204 | 118510 | +2.0% | 47 | 47 | 1920 | 1918 |
