#!/bin/sh
cnt=0
while true;
	do go test>out||break;
	cnt=`expr $cnt + 1`
	echo $cnt>cntres
	echo $cnt
	echo --------------------------------------------------
done
