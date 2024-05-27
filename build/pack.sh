sh build.sh $1 $2

if [ "$1" == "windows" ]; then
   tar -zcvf wineventtail-$2.tar.gz ../deploy/$1/$2/wineventtail.exe ../deploy/$1/*.ps1 ../deploy/$1/*.yml
   mv wineventtail-$2.tar.gz ../deploy/$1/$2/
else
   tar -zcvf dnslogtail-$2.tar.gz ../deploy/$1/$2/dnslogtail ../deploy/$1/*.sh ../deploy/$1/*.yml
   mv dnslogtail-$2.tar.gz ../deploy/$1/$2/
fi
